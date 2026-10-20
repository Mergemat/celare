package cli

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Mergemat/celare/internal/config"
	"github.com/Mergemat/celare/internal/library"
	"github.com/Mergemat/celare/internal/node"
	"github.com/spf13/cobra"
)

func (a *app) configPath() string { return filepath.Join(a.env.ConfigDir, "config.toml") }

// loadConfig reads and parses config.toml. It does not Check it; commands
// that need a valid config do that themselves.
func (a *app) loadConfig() (config.Config, error) {
	b, err := os.ReadFile(a.configPath())
	if errors.Is(err, os.ErrNotExist) {
		return config.Config{}, failure("config_missing", a.configPath()+" does not exist", "run `celare config init`")
	}
	if err != nil {
		return config.Config{}, err
	}
	cfg, err := config.Parse(b)
	if err != nil {
		return config.Config{}, failure("config_invalid", err.Error(), "fix it and run `celare config check`")
	}
	return cfg, nil
}

func table(w io.Writer) *tabwriter.Writer { return tabwriter.NewWriter(w, 0, 0, 2, ' ', 0) }

// ---- nodes

type nodeView struct {
	Name      string `json:"name"`
	Source    string `json:"source"`
	Protocol  string `json:"protocol"`
	Address   string `json:"address"`
	Port      int    `json:"port"`
	Transport string `json:"transport,omitempty"`
	Security  string `json:"security,omitempty"`
}

func viewOf(e library.Entry) nodeView {
	return nodeView{
		Name: e.Name, Source: e.Source, Protocol: e.Node.Protocol, Address: e.Node.Address, Port: e.Node.Port,
		Transport: e.Node.Transport.Type, Security: e.Node.Security.Type,
	}
}

func (a *app) nodesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "nodes",
		Short: "List, inspect, add, remove and test nodes",
		Long: `Nodes are proxy servers. Those added with 'nodes add' live in config.toml as
share links; subscription nodes come from 'subs update' and are named
<subscription>/<name>.`,
	}
	cmd.AddCommand(a.nodesListCmd(), a.nodesShowCmd(), a.nodesAddCmd(), a.nodesRemoveCmd(), a.nodesTestCmd())
	return cmd
}

func (a *app) entries() ([]library.Entry, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	return a.library().Nodes(cfg)
}

func (a *app) nodesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List every node",
		Args:  exactArgs(0, "no arguments"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			es, err := a.entries()
			if err != nil {
				return err
			}
			views := []nodeView{}
			for _, e := range es {
				views = append(views, viewOf(e))
			}
			next := []string{"celare connect <name> --confirm 30s"}
			if len(views) == 0 {
				next = []string{"celare nodes add <share-link>", "celare subs add <name> <url>"}
			}
			return a.ok(views, func(w io.Writer) {
				tw := table(w)
				fmt.Fprintln(tw, "NAME\tPROTOCOL\tSERVER\tTRANSPORT\tSECURITY\tSOURCE")
				for _, v := range views {
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", v.Name, v.Protocol,
						net.JoinHostPort(v.Address, fmt.Sprint(v.Port)), dash(v.Transport), dash(v.Security), v.Source)
				}
				tw.Flush()
			}, next...)
		},
	}
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func (a *app) nodesShowCmd() *cobra.Command {
	var reveal bool
	cmd := &cobra.Command{
		Use:   "show <node>",
		Short: "Show a node's parsed settings (credentials redacted unless --reveal)",
		Args:  exactArgs(1, "one node name"),
		RunE: func(cmd *cobra.Command, args []string) error {
			es, err := a.entries()
			if err != nil {
				return err
			}
			for _, e := range es {
				if e.Name != args[0] {
					continue
				}
				n := e.Node
				if !reveal {
					n = n.Redacted()
				}
				data := map[string]any{"name": e.Name, "source": e.Source, "node": n}
				return a.ok(data, func(w io.Writer) { printJSON(w, data) }, "celare connect "+quoteArg(e.Name))
			}
			return failure("node_not_found", fmt.Sprintf("no node named %q", args[0]), "run `celare nodes list` for the names")
		},
	}
	cmd.Flags().BoolVar(&reveal, "reveal", false, "show passwords, UUIDs and private keys")
	return cmd
}

func (a *app) nodesAddCmd() *cobra.Command {
	var name string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "add <share-link>",
		Short: "Add a node from a share link (vless://, vmess://, trojan://, ss://, hysteria2://, wireguard://)",
		Long: `Add a node to config.toml from a share link. The name defaults to the link's
#fragment. Quote the link: it contains & and ?.`,
		Args: exactArgs(1, "one share link"),
		RunE: func(cmd *cobra.Command, args []string) error {
			link := strings.TrimSpace(args[0])
			n, err := node.ParseLink(link)
			if err != nil {
				return failure("bad_link", err.Error(), "pass the link exactly as the panel shows it, in single quotes")
			}
			if name == "" {
				name = strings.ReplaceAll(n.Name, "/", "-")
			}
			if name == "" {
				name = n.Protocol + "-" + n.Address
			}
			entry := config.NodeEntry{Name: name, Link: link}
			data := map[string]any{"name": name, "node": n.Redacted(), "dryRun": dryRun}
			if !dryRun {
				if err := config.AddNode(a.configPath(), entry); err != nil {
					return failure("config_edit_failed", err.Error(), "")
				}
			}
			return a.ok(data, func(w io.Writer) {
				verb := "added"
				if dryRun {
					verb = "would add"
				}
				fmt.Fprintf(w, "%s node %q (%s %s:%d)\n", verb, name, n.Protocol, n.Address, n.Port)
			}, "celare connect "+quoteArg(name)+" --confirm 30s")
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "node name (default: the link's #fragment)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be added without writing config.toml")
	return cmd
}

func (a *app) nodesRemoveCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "remove <node>",
		Short: "Remove a node added with 'nodes add'",
		Args:  exactArgs(1, "one node name"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if sub, _, ok := strings.Cut(args[0], "/"); ok {
				return failure("node_from_subscription", fmt.Sprintf("%q comes from subscription %q, so it can't be removed on its own", args[0], sub),
					fmt.Sprintf("remove it in the panel, or drop the whole subscription with `celare subs remove %s`", sub))
			}
			if !dryRun {
				if err := config.RemoveNode(a.configPath(), args[0]); err != nil {
					return failure("node_not_found", err.Error(), "")
				}
			}
			return a.ok(map[string]any{"name": args[0], "dryRun": dryRun}, func(w io.Writer) {
				fmt.Fprintf(w, "removed node %q\n", args[0])
			})
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "check the node exists without writing config.toml")
	return cmd
}

// ---- subs

func (a *app) subsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "subs",
		Short: "Manage subscriptions (panel URLs that list nodes)",
	}
	cmd.AddCommand(a.subsListCmd(), a.subsAddCmd(), a.subsUpdateCmd(), a.subsRemoveCmd())
	return cmd
}

type subView struct {
	Name      string         `json:"name"`
	URL       string         `json:"url"`
	Nodes     int            `json:"nodes"`
	UpdatedAt *time.Time     `json:"updatedAt"`
	Usage     *library.Usage `json:"usage,omitempty"`
	Problems  []string       `json:"problems,omitempty"`
}

// redactURL keeps the host and hides the path, which carries the token.
func redactURL(raw string) string {
	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok {
		return "<redacted>"
	}
	host, _, _ := strings.Cut(rest, "/")
	return scheme + "://" + host + "/<redacted>"
}

func (a *app) subsListCmd() *cobra.Command {
	var reveal bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List subscriptions with node counts, last update and traffic usage",
		Args:  exactArgs(0, "no arguments"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			views := []subView{}
			for _, s := range cfg.Subscriptions {
				v := subView{Name: s.Name, URL: s.URL}
				if !reveal {
					v.URL = redactURL(s.URL)
				}
				if info, ok := a.library().SubscriptionInfo(s.Name); ok {
					t := info.UpdatedAt
					v.Nodes, v.UpdatedAt, v.Usage, v.Problems = info.Nodes, &t, info.Usage, info.Problems
				}
				views = append(views, v)
			}
			return a.ok(views, func(w io.Writer) {
				tw := table(w)
				fmt.Fprintln(tw, "NAME\tNODES\tUPDATED\tUSED\tURL")
				for _, v := range views {
					updated, used := "never", "-"
					if v.UpdatedAt != nil {
						updated = v.UpdatedAt.Local().Format("2006-01-02 15:04")
					}
					if u := v.Usage; u != nil {
						used = humanBytes(u.UploadBytes + u.DownloadBytes)
						if u.TotalBytes > 0 {
							used += " / " + humanBytes(u.TotalBytes)
						}
					}
					fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\n", v.Name, v.Nodes, updated, used, v.URL)
				}
				tw.Flush()
			}, "celare subs update")
		},
	}
	cmd.Flags().BoolVar(&reveal, "reveal", false, "show full URLs, which carry the access token")
	return cmd
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func (a *app) subsAddCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "add <name> <url>",
		Short: "Add a subscription and fetch its nodes",
		Args:  exactArgs(2, "a name and a URL"),
		RunE: func(cmd *cobra.Command, args []string) error {
			sub := config.Subscription{Name: args[0], URL: args[1]}
			if strings.Contains(sub.Name, "/") {
				return failure("bad_name", "subscription names can't contain '/'", "")
			}
			if dryRun {
				return a.ok(map[string]any{"name": sub.Name, "url": redactURL(sub.URL), "dryRun": true}, func(w io.Writer) {
					fmt.Fprintf(w, "would add subscription %q\n", sub.Name)
				})
			}
			info, fetchErr := a.library().UpdateSubscription(cmd.Context(), sub)
			if fetchErr != nil {
				return failure("subscription_fetch_failed", fetchErr.Error(), "check the URL; nothing was added")
			}
			if err := config.AddSubscription(a.configPath(), sub); err != nil {
				return failure("config_edit_failed", err.Error(), "")
			}
			return a.ok(info, func(w io.Writer) {
				fmt.Fprintf(w, "added subscription %q with %d nodes\n", sub.Name, info.Nodes)
			}, "celare nodes list")
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate without fetching or writing config.toml")
	return cmd
}

func (a *app) subsUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update [name...]",
		Short: "Fetch subscriptions again (all of them by default)",
		Long: `Fetch subscriptions again. A failed fetch keeps the previous nodes. The
running tunnel keeps its node until 'celare apply' or 'celare connect'.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			want := map[string]bool{}
			for _, n := range args {
				want[n] = true
			}
			type result struct {
				library.SubscriptionInfo
				Error string `json:"error,omitempty"`
			}
			results := []result{}
			failed := 0
			for _, s := range cfg.Subscriptions {
				if len(want) > 0 && !want[s.Name] {
					continue
				}
				delete(want, s.Name)
				info, err := a.library().UpdateSubscription(cmd.Context(), s)
				r := result{SubscriptionInfo: info}
				r.Name = s.Name
				if err != nil {
					r.Error = err.Error()
					failed++
				}
				results = append(results, r)
			}
			for n := range want {
				return failure("subscription_not_found", fmt.Sprintf("no subscription named %q", n), "run `celare subs list`")
			}
			if failed > 0 && failed == len(results) {
				return failure("subscription_fetch_failed", results[0].Error, "previous nodes were kept")
			}
			return a.ok(results, func(w io.Writer) {
				for _, r := range results {
					if r.Error != "" {
						fmt.Fprintf(w, "%s: failed, kept previous nodes: %s\n", r.Name, r.Error)
						continue
					}
					fmt.Fprintf(w, "%s: %d nodes\n", r.Name, r.Nodes)
				}
			}, "celare nodes list")
		},
	}
}

func (a *app) subsRemoveCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a subscription and its cached nodes",
		Args:  exactArgs(1, "one subscription name"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !dryRun {
				if err := config.RemoveSubscription(a.configPath(), args[0]); err != nil {
					return failure("subscription_not_found", err.Error(), "")
				}
				for _, ext := range []string{".txt", ".json"} {
					os.Remove(filepath.Join(a.env.ConfigDir, "subscriptions", args[0]+ext))
				}
			}
			return a.ok(map[string]any{"name": args[0], "dryRun": dryRun}, func(w io.Writer) {
				fmt.Fprintf(w, "removed subscription %q\n", args[0])
			})
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "check without writing config.toml")
	return cmd
}
