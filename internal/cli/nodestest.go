package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Mergemat/celare/internal/library"
	"github.com/Mergemat/celare/internal/netconf"
	"github.com/Mergemat/celare/internal/probe"
	"github.com/Mergemat/celare/internal/system"
	"github.com/spf13/cobra"
)

type testResult struct {
	Node string `json:"node"`
	probe.Result
}

func (a *app) nodesTestCmd() *cobra.Command {
	var target string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "test [node...]",
		Short: "Measure each node by fetching a URL through it (all nodes by default)",
		Long: `Start a private Xray instance per node and time one request through it,
leaving by the physical network interface: the same path celare's tunnel
uses, whatever other VPN or tunnel is running. Needs no daemon and doesn't
touch the running tunnel.`,
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
			// Test the path the tunnel will use: straight out of the
			// physical interface. Through another VPN a node can look fine
			// while the network blocks it directly.
			sys := &system.Tunnel{Run: system.Exec}
			route, _, err := sys.Physical()
			if err != nil {
				return failure("no_network", err.Error(), "connect Wi-Fi or Ethernet")
			}
			iface := route.Interface
			ours := ""
			if t, err := a.client().Tunnel(cmd.Context()); err == nil && t.System != nil {
				ours = t.System.Tun
			}
			var warning string
			if out, err := system.Exec("netstat", "-rn", "-f", "inet"); err == nil {
				if foreign := netconf.ForeignTunnels(netconf.ParseDefaultRoutes(out), ours); len(foreign) > 0 {
					warning = fmt.Sprintf("another VPN is active (%s); its firewall can block direct traffic, so results may all fail until you quit it", strings.Join(foreign, ", "))
				}
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
					results[i] = testResult{Node: e.Name, Result: probe.Node(cmd.Context(), e.Node, target, timeout, iface)}
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
			data := map[string]any{"interface": iface, "results": results}
			if warning != "" {
				data["warning"] = warning
			}
			return a.ok(data, func(w io.Writer) {
				fmt.Fprintf(w, "direct over %s:\n", iface)
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
				if warning != "" {
					fmt.Fprintln(w, "warning: "+warning)
				}
			}, next...)
		},
	}
	cmd.Flags().StringVar(&target, "url", probe.DefaultURL, "URL to fetch through each node")
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
