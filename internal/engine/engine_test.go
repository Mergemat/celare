package engine_test

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/b-omarov/celare/internal/config"
	"github.com/b-omarov/celare/internal/engine"
	"github.com/b-omarov/celare/internal/node"
	"github.com/b-omarov/celare/internal/xrayconf"
)

// The "server" is a second in-process Xray whose freedom outbound
// redirects every proxied connection to a target that answers
// "proxied". Traffic that goes direct reaches a target answering
// "direct". So the response body says which path a request took.
func TestRoutingSendsTrafficDownTheRightPath(t *testing.T) {
	direct := answer(t, "direct")
	proxied := answer(t, "proxied")
	serverPort := freePort(t)
	startEngine(t, fmt.Sprintf(`{
		"inbounds": [{"listen": "127.0.0.1", "port": %d, "protocol": "vless",
			"settings": {"clients": [{"id": "22222222-3333-4444-9555-666666666666"}], "decryption": "none"}}],
		"outbounds": [{"protocol": "freedom", "settings": {"redirect": "%s"}}]
	}`, serverPort, proxied))

	socks := freePort(t)
	cfg, err := xrayconf.Config(xrayconf.Plan{
		Node: node.Node{
			Protocol: "vless", Address: "127.0.0.1", Port: serverPort,
			UUID: "22222222-3333-4444-9555-666666666666", Encryption: "none",
		},
		Default: config.Proxy,
		Rules: []config.Rule{
			{Action: config.Direct, Domains: []string{"full:localhost"}},
			{Action: config.Block, IPs: []string{"::1/128"}},
		},
		SOCKSPort: socks,
	})
	if err != nil {
		t.Fatal(err)
	}
	startEngine(t, string(cfg))

	_, directPort, _ := net.SplitHostPort(direct)
	client := socksClient(t, socks)
	if got := get(t, client, "http://localhost:"+directPort); got != "direct" {
		t.Errorf("localhost (direct rule) answered %q, want direct", got)
	}
	if got := get(t, client, "http://127.0.0.1:"+directPort); got != "proxied" {
		t.Errorf("127.0.0.1 (no rule, default proxy) answered %q, want proxied", got)
	}
	if got := get(t, client, "http://[::1]:"+directPort); got != "" {
		t.Errorf("[::1] (block rule) answered %q, want no answer", got)
	}
}

func startEngine(t *testing.T, cfg string) {
	t.Helper()
	e, err := engine.Start([]byte(cfg))
	if err != nil {
		t.Fatalf("start engine: %v\n%s", err, cfg)
	}
	t.Cleanup(func() { e.Close() })
}

// answer starts an HTTP server on 127.0.0.1 that replies with body and
// returns its host:port.
func answer(t *testing.T, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

func socksClient(t *testing.T, port int) *http.Client {
	t.Helper()
	proxyURL, _ := url.Parse(fmt.Sprintf("socks5://127.0.0.1:%d", port))
	return &http.Client{
		// A fresh REALITY server holds its first connection for up to 5s
		// while it probes its target.
		Timeout:   10 * time.Second,
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true},
	}
}

// get returns the response body, or "" when the request fails.
func get(t *testing.T, c *http.Client, u string) string {
	t.Helper()
	resp, err := c.Get(u)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
