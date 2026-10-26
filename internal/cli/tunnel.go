package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Mergemat/celare/internal/api"
	"github.com/Mergemat/celare/internal/daemon"
	"github.com/spf13/cobra"
)

func (a *app) statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the tunnel, any pending change, and warnings",
		Args:  exactArgs(0, "no arguments"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			t, err := a.client().Tunnel(cmd.Context())
			if err != nil {
				return err
			}
			var next []string
			switch {
			case t.Pending != nil:
				next = pendingNext(*t.Pending)
			case t.State == "down":
				next = []string{"celare connect <node>", "celare nodes list"}
			}
			return a.ok(t, func(w io.Writer) { printTunnel(w, t) }, next...)
		},
	}
}

func printTunnel(w io.Writer, t api.Tunnel) {
	since := t.Since.Local().Format("15:04")
	if t.State == "up" {
		line := fmt.Sprintf("up · %s · since %s", t.Node, since)
		if t.System != nil && t.System.Interface != "" {
			line += fmt.Sprintf(" · %s → %s", t.System.Interface, t.System.Tun)
		}
		fmt.Fprintln(w, line)
	} else {
		fmt.Fprintf(w, "down · since %s\n", since)
	}
	if p := t.Pending; p != nil {
		fmt.Fprintf(w, "pending %s: %s; reverts to %s at %s unless confirmed\n",
			p.ID, p.Action, endpointName(p.From), p.Deadline.Local().Format("15:04:05"))
	}
}

func endpointName(e *daemon.Endpoint) string {
	if e == nil {
		return "down"
	}
	return e.Node
}

type changeFlags struct {
	confirm string
	probe   string
	dryRun  bool
}

func (f *changeFlags) register(cmd *cobra.Command, probe bool) {
	cmd.Flags().StringVar(&f.confirm, "confirm", "", "revert unless `celare confirm <id>` runs within this duration (e.g. 30s)")
	if probe {
		cmd.Flags().StringVar(&f.probe, "probe", "", "fetch this URL through the new tunnel; revert at once if it fails")
	}
	cmd.Flags().BoolVar(&f.dryRun, "dry-run", false, "show the planned change without making it")
}

func (a *app) change(cmd *cobra.Command, action daemon.Action, nodeName string, f changeFlags) error {
	if action != daemon.Disconnect {
		if cfg, err := a.loadConfig(); err == nil {
			a.warnings = a.dnsWarnings(cfg)
		}
	}
	c, err := a.client().CreateChange(cmd.Context(), api.ChangeRequest{
		Action: action, Node: nodeName, Confirm: f.confirm, Probe: f.probe, DryRun: f.dryRun,
	})
	if err != nil {
		return err
	}
	var next []string
	switch c.State {
	case daemon.Pending:
		next = pendingNext(c)
	case daemon.Planned:
		run := "celare " + string(action)
		if nodeName != "" {
			run += " " + quoteArg(nodeName)
		}
		next = []string{run}
	}
	return a.ok(c, func(w io.Writer) { printChange(w, c) }, next...)
}

func pendingNext(c daemon.Change) []string {
	return []string{"celare confirm " + c.ID, "celare revert " + c.ID}
}

func printChange(w io.Writer, c daemon.Change) {
	switch c.State {
	case daemon.Planned:
		fmt.Fprintf(w, "plan: %s, %s → %s\n", c.Action, endpointName(c.From), endpointName(c.To))
	case daemon.Pending:
		fmt.Fprintf(w, "%s pending: %s → %s; reverts at %s unless confirmed\n",
			c.ID, endpointName(c.From), endpointName(c.To), c.Deadline.Local().Format("15:04:05"))
	default:
		fmt.Fprintf(w, "%s %s: %s → %s\n", c.ID, c.State, endpointName(c.From), endpointName(c.To))
	}
	if c.Reason != "" {
		fmt.Fprintln(w, "  "+c.Reason)
	}
	printDiff(w, c.Diff)
}

func printDiff(w io.Writer, d daemon.Diff) {
	if d.Default != nil {
		fmt.Fprintf(w, "  default: %s → %s\n", d.Default.From, d.Default.To)
	}
	if d.DNS != nil {
		fmt.Fprintf(w, "  dns: %v → %v\n", d.DNS.From, d.DNS.To)
	}
	if r := d.Rules; r != nil {
		for _, rule := range r.Added {
			fmt.Fprintf(w, "  + rule %s\n", ruleSummary(rule))
		}
		for _, rule := range r.Removed {
			fmt.Fprintf(w, "  - rule %s\n", ruleSummary(rule))
		}
		if r.Reordered {
			fmt.Fprintln(w, "  rules reordered")
		}
	}
}

func (a *app) connectCmd() *cobra.Command {
	var f changeFlags
	cmd := &cobra.Command{
		Use:   "connect [node]",
		Short: "Bring the tunnel up through a node (default: the last one)",
		Long: `Bring the tunnel up through a node, or switch to another node while up.
Without a node, reconnects the last one. Reads config.toml as it is now.

Over a connection you depend on, make it provisional:
  celare connect main/Tokyo --confirm 30s --probe https://www.gstatic.com/generate_204
then 'celare confirm <id>' once you have checked it works.`,
		Args: maxArgs(1, "one node name"),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			return a.change(cmd, daemon.Connect, name, f)
		},
	}
	f.register(cmd, true)
	return cmd
}

func (a *app) disconnectCmd() *cobra.Command {
	var f changeFlags
	cmd := &cobra.Command{
		Use:   "disconnect",
		Short: "Take the tunnel down",
		Args:  exactArgs(0, "no arguments"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.change(cmd, daemon.Disconnect, "", f)
		},
	}
	f.register(cmd, false)
	return cmd
}

func (a *app) applyCmd() *cobra.Command {
	var f changeFlags
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Push config.toml (rules, DNS) to the running tunnel",
		Long: `Push config.toml's rules, default and DNS to the running tunnel. Editing
config.toml, or running nodes/subs/rules commands, never affects traffic
until apply. Use --dry-run to see the diff first.`,
		Args: exactArgs(0, "no arguments"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.change(cmd, daemon.Apply, "", f)
		},
	}
	f.register(cmd, true)
	return cmd
}

func (a *app) confirmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "confirm [id]",
		Short: "Keep the pending change",
		Long:  "Keep the pending change. Pass the id the change printed so you never confirm someone else's change.",
		Args:  maxArgs(1, "one change id"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client().Confirm(cmd.Context(), firstArg(args))
			if err != nil {
				return err
			}
			return a.ok(c, func(w io.Writer) { printChange(w, c) })
		},
	}
}

func (a *app) revertCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "revert [id]",
		Short: "Undo the pending change now",
		Args:  maxArgs(1, "one change id"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client().Revert(cmd.Context(), firstArg(args))
			if err != nil {
				return err
			}
			return a.ok(c, func(w io.Writer) { printChange(w, c) })
		},
	}
}

func (a *app) changesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "changes",
		Short: "List recent changes and how each ended",
		Args:  exactArgs(0, "no arguments"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			cs, err := a.client().Changes(cmd.Context())
			if err != nil {
				return err
			}
			if cs == nil {
				cs = []daemon.Change{}
			}
			return a.ok(cs, func(w io.Writer) {
				if len(cs) == 0 {
					fmt.Fprintln(w, "no changes yet")
				}
				tw := table(w)
				fmt.Fprintln(tw, "ID\tSTATE\tACTION\tFROM → TO\tAT\tREASON")
				for _, c := range cs {
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s → %s\t%s\t%s\n", c.ID, c.State, c.Action,
						endpointName(c.From), endpointName(c.To), c.CreatedAt.Local().Format(time.TimeOnly), c.Reason)
				}
				tw.Flush()
			})
		},
	}
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

// quoteArg quotes a shell argument when it needs it.
func quoteArg(s string) string {
	if strings.ContainsAny(s, " '\"$`\\()[]{}*?!&|;<>") {
		return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	}
	return s
}
