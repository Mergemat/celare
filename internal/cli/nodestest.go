package cli

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"

	"github.com/Mergemat/celare/internal/config"
	"github.com/Mergemat/celare/internal/engine"
	"github.com/Mergemat/celare/internal/library"
	"github.com/Mergemat/celare/internal/xrayconf"
	"github.com/spf13/cobra"
)

type testResult struct {
	Node      string `json:"node"`
	OK        bool   `json:"ok"`
	LatencyMs int64  `json:"latencyMs,omitempty"`
	Status    int    `json:"status,omitempty"`
	Error     string `json:"error,omitempty"`
}

func (a *app) nodesTestCmd() *cobra.Command {
	var target string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "test [node...]",
		Short: "Measure each node by fetching a URL through it (all nodes by default)",
		Long: `Start a private Xray instance per node, outside the tunnel, and time one
request through it. Needs no daemon and doesn't touch the running tunnel.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			es, err := a.entries()
			if err != nil {
				return err
			}
			if len(args) > 0 {
				es, err = pick(es, args)
				if err != nil {
					return err
				}
			}
			// Inside celare's own tunnel, test traffic must leave by the
			// physical interface or it would be measured through the tunnel.
			// Otherwise it takes whatever path the system uses.
			iface := ""
			if t, err := a.client().Tunnel(cmd.Context()); err == nil && t.State == "up" && t.System != nil {
				iface = t.System.Interface
			}
			results := make([]testResult, len(es))
			var wg sync.WaitGroup
			sem := make(chan struct{}, 6)
			for i, e := range es {
				wg.Add(1)
				go func() {
					defer wg.Done()
					sem <- struct{}{}
					defer func() { <-sem }()
					results[i] = testNode(cmd.Context(), e, target, timeout, iface)
				}()
			}
			wg.Wait()
			sort.SliceStable(results, func(i, j int) bool {
				if results[i].OK != results[j].OK {
					return results[i].OK
				}
				return results[i].LatencyMs < results[j].LatencyMs
			})
			var next []string
			if len(results) > 0 && results[0].OK {
				next = []string{"celare connect " + quoteArg(results[0].Node) + " --confirm 30s"}
			}
			return a.ok(results, func(w io.Writer) {
				tw := table(w)
				fmt.Fprintln(tw, "NODE\tRESULT")
				for _, r := range results {
					res := fmt.Sprintf("%d ms", r.LatencyMs)
					if !r.OK {
						res = "failed: " + r.Error
					}
					fmt.Fprintf(tw, "%s\t%s\n", r.Node, res)
				}
				tw.Flush()
			}, next...)
		},
	}
	cmd.Flags().StringVar(&target, "url", "https://www.gstatic.com/generate_204", "URL to fetch through each node")
	cmd.Flags().DurationVar(&timeout, "timeout", 8*time.Second, "give up on a node after this long")
	return cmd
}

func pick(es []library.Entry, names []string) ([]library.Entry, error) {
	byName := map[string]library.Entry{}
	for _, e := range es {
		byName[e.Name] = e
	}
	var out []library.Entry
	for _, n := range names {
		e, ok := byName[n]
		if !ok {
			return nil, failure("node_not_found", fmt.Sprintf("no node named %q", n), "run `celare nodes list` for the names")
		}
		out = append(out, e)
	}
	return out, nil
}

func testNode(ctx context.Context, e library.Entry, target string, timeout time.Duration, iface string) testResult {
	r := testResult{Node: e.Name}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		r.Error = err.Error()
		return r
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	cfg, err := xrayconf.Config(xrayconf.Plan{
		Node: e.Node, Default: config.Proxy, SOCKSPort: port, BindInterface: iface, LogLevel: "none",
	})
	if err != nil {
		r.Error = err.Error()
		return r
	}
	eng, err := engine.Start(cfg)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	defer eng.Close()
	proxyURL, _ := url.Parse(fmt.Sprintf("socks5://127.0.0.1:%d", port))
	client := &http.Client{Timeout: timeout, Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	resp.Body.Close()
	r.OK, r.Status, r.LatencyMs = true, resp.StatusCode, time.Since(start).Milliseconds()
	return r
}
