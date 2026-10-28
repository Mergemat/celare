package cli

import (
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/Mergemat/celare/internal/library"
	"github.com/Mergemat/celare/internal/probe"
	"github.com/Mergemat/celare/internal/system"
	"github.com/spf13/cobra"
)

type testResult struct {
	Node      string `json:"node"`
	Interface string `json:"interface,omitempty"`
	probe.Result
}

func (a *app) nodesTestCmd() *cobra.Command {
	var target string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "test [node...]",
		Short: "Measure each node by fetching a URL through it (all nodes by default)",
		Long: `Start a private Xray instance per node and time one request through it,
leaving by the same path celare's tunnel uses: the system's current route
out for the server's IP family. Needs no daemon
and doesn't touch the running tunnel.`,
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
			// Test the path the tunnel will use: the upstream, skipping
			// celare's own tunnel if it is up.
			ours := ""
			if t, err := a.client().Tunnel(cmd.Context()); err == nil && t.System != nil {
				ours = t.System.Tun
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
					n, up, err := (&system.Tunnel{}).ResolveUpstream(e.Node, ours)
					if err != nil {
						results[i] = testResult{Node: e.Name, Result: probe.Result{Error: err.Error()}}
						return
					}
					results[i] = testResult{Node: e.Name, Interface: up.Interface, Result: probe.Node(cmd.Context(), n, target, timeout, up.Interface)}
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
			data := map[string]any{"results": results}
			return a.ok(data, func(w io.Writer) {
				tw := table(w)
				fmt.Fprintln(tw, "NODE\tINTERFACE\tRESULT")
				for _, r := range results {
					res := fmt.Sprintf("%d ms", r.LatencyMs)
					if !r.OK {
						res = "failed: " + r.Error
					}
					fmt.Fprintf(tw, "%s\t%s\t%s\n", r.Node, r.Interface, res)
				}
				tw.Flush()
			}, next...)
		},
	}
	cmd.Flags().StringVar(&target, "url", probe.DefaultURL, "URL to fetch through each node")
	cmd.Flags().DurationVar(&timeout, "timeout", 3*time.Second, "give up on a node after this long")
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
