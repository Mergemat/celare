// Package probe checks whether a node carries traffic by running a
// private Xray instance with a SOCKS inbound and fetching a URL through
// it. Bound to the physical interface, it tests the exact path celare's
// tunnel uses, regardless of any other VPN or celare's own routes.
package probe

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Mergemat/celare/internal/config"
	"github.com/Mergemat/celare/internal/engine"
	"github.com/Mergemat/celare/internal/node"
	"github.com/Mergemat/celare/internal/xrayconf"
)

// DefaultURL answers 204 quickly from everywhere it is reachable.
const DefaultURL = "https://www.gstatic.com/generate_204"

type Result struct {
	OK        bool   `json:"ok"`
	LatencyMs int64  `json:"latencyMs,omitempty"`
	Status    int    `json:"status,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Node fetches target through n, leaving by iface ("" means the system's
// normal path).
func Node(ctx context.Context, n node.Node, target string, timeout time.Duration, iface string) Result {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return Result{Error: err.Error()}
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	cfg, err := xrayconf.Config(xrayconf.Plan{
		Node: n, Default: config.Proxy, SOCKSPort: port, BindInterface: iface, LogLevel: "none",
	})
	if err != nil {
		return Result{Error: err.Error()}
	}
	eng, err := engine.Start(cfg)
	if err != nil {
		return Result{Error: err.Error()}
	}
	defer eng.Close()
	proxyURL, _ := url.Parse(fmt.Sprintf("socks5://127.0.0.1:%d", port))
	client := &http.Client{Timeout: timeout, Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return Result{Error: err.Error()}
	}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return Result{Error: Explain(err, timeout)}
	}
	resp.Body.Close()
	return Result{OK: true, Status: resp.StatusCode, LatencyMs: time.Since(start).Milliseconds()}
}

// Explain turns network errors into what happened, in words.
func Explain(err error, timeout time.Duration) string {
	var netErr net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return fmt.Sprintf("no response within %s", timeout.Round(time.Second))
	case strings.Contains(err.Error(), "EOF"), strings.Contains(err.Error(), "connection reset"):
		return "the server closed the connection"
	case strings.Contains(err.Error(), "connection refused"):
		return "connection refused"
	default:
		return err.Error()
	}
}
