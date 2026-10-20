package cli

import (
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"

	"github.com/Mergemat/celare/internal/config"
	"github.com/Mergemat/celare/internal/engine"
	"github.com/Mergemat/celare/internal/node"
	"github.com/Mergemat/celare/internal/xrayconf"
	"github.com/spf13/cobra"
)

func (a *app) rulesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rules",
		Short: "List, add, remove and explain routing rules",
		Long: `Rules send matching traffic to proxy, direct or block. They are checked top
to bottom and the first match wins; unmatched traffic goes to config.toml's
default (proxy unless set). Changes apply on 'celare apply'.

Matches:
  domain:example.com   the domain and its subdomains
  full:example.com     exactly that host
  keyword:google       any host containing the word
  regexp:^ads\.        a regular expression
  geosite:category-ads-all, geoip:ru   lists from geosite.dat / geoip.dat
  203.0.113.0/24, 2001:db8::/32, 198.51.100.7   IPs and CIDRs`,
	}
	cmd.AddCommand(a.rulesListCmd(), a.rulesAddCmd(), a.rulesRemoveCmd(), a.rulesExplainCmd(), a.rulesGeoUpdateCmd())
	return cmd
}

type ruleView struct {
	Position int `json:"position"`
	config.Rule
}

func ruleSummary(r config.Rule) string {
	s := string(r.Action) + " " + strings.Join(append(append([]string{}, r.Domains...), r.IPs...), " ")
	if r.Name != "" {
		s = r.Name + ": " + s
	}
	return s
}

func (a *app) rulesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List rules in match order",
		Args:  exactArgs(0, "no arguments"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			views := []ruleView{}
			for i, r := range cfg.Rules {
				views = append(views, ruleView{Position: i + 1, Rule: r})
			}
			def := cfg.Default
			if def == "" {
				def = config.Proxy
			}
			data := map[string]any{"rules": views, "default": def}
			return a.ok(data, func(w io.Writer) {
				tw := table(w)
				fmt.Fprintln(tw, "#\tNAME\tACTION\tMATCH")
				for _, v := range views {
					fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n", v.Position, dash(v.Name), v.Action,
						strings.Join(append(append([]string{}, v.Domains...), v.IPs...), " "))
				}
				fmt.Fprintf(tw, "-\tdefault\t%s\teverything else\n", def)
				tw.Flush()
			})
		},
	}
}

// classify splits matches into Xray domain and IP matchers. A bare domain
// becomes domain:x, because Xray would otherwise treat it as a substring.
func classify(matches []string) (domains, ips []string, err error) {
	for _, m := range matches {
		switch {
		case strings.HasPrefix(m, "geoip:"):
			ips = append(ips, m)
		case net.ParseIP(m) != nil:
			ips = append(ips, m)
		case strings.Contains(m, "/") && !strings.Contains(m, ":/"):
			if _, _, err := net.ParseCIDR(m); err != nil {
				return nil, nil, fmt.Errorf("%q looks like a CIDR but isn't one", m)
			}
			ips = append(ips, m)
		case hasDomainPrefix(m):
			domains = append(domains, m)
		case strings.Contains(m, ":"):
			return nil, nil, fmt.Errorf("%q has an unknown prefix; use domain:, full:, keyword:, regexp:, geosite: or geoip:", m)
		default:
			domains = append(domains, "domain:"+m)
		}
	}
	return domains, ips, nil
}

func hasDomainPrefix(m string) bool {
	for _, p := range []string{"domain:", "full:", "keyword:", "regexp:", "geosite:", "ext:"} {
		if strings.HasPrefix(m, p) {
			return true
		}
	}
	return false
}

func (a *app) rulesAddCmd() *cobra.Command {
	var name string
	var first, dryRun bool
	cmd := &cobra.Command{
		Use:   "add <proxy|direct|block> <match>...",
		Short: "Add a rule (last by default, --first to win over the others)",
		Example: `  celare rules add proxy domain:openai.com domain:anthropic.com --name ai --first
  celare rules add direct geoip:ru geosite:ru
  celare rules add block geosite:category-ads-all`,
		Args: minArgs(2, "an action and at least one match"),
		RunE: func(cmd *cobra.Command, args []string) error {
			action := config.Action(args[0])
			switch action {
			case config.Proxy, config.Direct, config.Block:
			default:
				return usageError{fmt.Errorf("action %q is not one of proxy, direct, block", args[0])}
			}
			domains, ips, err := classify(args[1:])
			if err != nil {
				return usageError{err}
			}
			rule := config.Rule{Name: name, Action: action, Domains: domains, IPs: ips}
			if !dryRun {
				if err := config.AddRule(a.configPath(), rule, first); err != nil {
					return failure("config_edit_failed", err.Error(), "")
				}
			}
			return a.ok(map[string]any{"rule": rule, "first": first, "dryRun": dryRun}, func(w io.Writer) {
				fmt.Fprintf(w, "added rule %s\n", ruleSummary(rule))
			}, "celare apply --dry-run", "celare apply --confirm 30s")
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "name the rule so it can be removed by name")
	cmd.Flags().BoolVar(&first, "first", false, "put the rule before all others")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show the rule without writing config.toml")
	return cmd
}

func (a *app) rulesRemoveCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "remove <name|position>",
		Short: "Remove a rule by name or by its position in 'rules list'",
		Args:  exactArgs(1, "a rule name or position"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !dryRun {
				if err := config.RemoveRule(a.configPath(), args[0]); err != nil {
					return failure("rule_not_found", err.Error(), "")
				}
			}
			return a.ok(map[string]any{"rule": args[0], "dryRun": dryRun}, func(w io.Writer) {
				fmt.Fprintf(w, "removed rule %s\n", args[0])
			}, "celare apply --dry-run")
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "check without writing config.toml")
	return cmd
}

type explanation struct {
	engine.Decision
	Action config.Action `json:"action"`
	// Rule is the rule that matched; nil when the default decided.
	Rule *ruleView `json:"rule"`
}

func (a *app) rulesExplainCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "explain <host|ip>[:port]",
		Short: "Show which rule decides where traffic to a target goes",
		Long: `Ask Xray's own router, built from config.toml as it is now, where a
connection would go. Works offline and without the tunnel. Domains are also
resolved so IP rules are checked as they are for real traffic.`,
		Args: exactArgs(1, "one target"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			if problems := cfg.Check(); len(problems) > 0 {
				return &apiError{Code: "config_invalid", Message: "config.toml has problems", Problems: problems, Hint: "fix them and run `celare config check`"}
			}
			def := cfg.Default
			if def == "" {
				def = config.Proxy
			}
			xray, err := xrayconf.Config(xrayconf.Plan{
				// Routing doesn't depend on the node; any valid one will do.
				Node:    node.Node{Protocol: "trojan", Address: "127.0.0.1", Port: 1, Password: "x"},
				Default: def,
				Rules:   cfg.Rules,
				TUN:     &xrayconf.TUN{Name: "utun99", MTU: 1500},
			})
			if err != nil {
				return failure("config_invalid", err.Error(), "")
			}
			d, err := engine.Explain(xray, args[0])
			if err != nil {
				return failure("explain_failed", err.Error(), "")
			}
			ex := explanation{Decision: d, Action: config.Action(d.Outbound)}
			if pos, ok := strings.CutPrefix(d.RuleTag, "rule-"); ok {
				if i, err := strconv.Atoi(pos); err == nil && i >= 1 && i <= len(cfg.Rules) {
					ex.Rule = &ruleView{Position: i, Rule: cfg.Rules[i-1]}
				}
			}
			return a.ok(ex, func(w io.Writer) {
				by := "the default"
				if ex.Rule != nil {
					by = fmt.Sprintf("rule %d (%s)", ex.Rule.Position, ruleSummary(ex.Rule.Rule))
				}
				fmt.Fprintf(w, "%s → %s, by %s\n", args[0], ex.Action, by)
				if len(d.IPs) > 0 && d.Domain != "" {
					fmt.Fprintf(w, "  resolved to %s\n", strings.Join(d.IPs, ", "))
				}
			})
		},
	}
}

func printJSON(w io.Writer, v any) { encodeJSON(w, v) }
