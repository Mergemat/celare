package cli

import (
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"

	"github.com/Mergemat/celare/internal/config"
	"github.com/Mergemat/celare/internal/engine"
	"github.com/spf13/cobra"
)

func (a *app) rulesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rules",
		Short: "List, add, remove and explain routing rules",
		Long: `Rules send matching traffic to proxy, direct or block. They are checked top
to bottom and the first match wins; unmatched traffic goes to config.toml's
default (proxy unless set). Changes apply on 'celare apply'.

A rule matches traffic to any of its matches:
  domain:example.com   the domain and its subdomains
  full:example.com     exactly that host
  keyword:google       any host containing the word
  regexp:^ads\.        a regular expression
  geosite:category-ads-all, geoip:ru   lists from geosite.dat / geoip.dat
  203.0.113.0/24, 2001:db8::/32, 198.51.100.7   IPs and CIDRs

--port and --network narrow a rule: traffic must also use them. Without
matches, the rule applies to every destination on that port or network:
  celare rules add block --network udp --port 443   QUIC falls back to TCP
  celare rules add direct --port 22                 SSH skips the proxy

--dns names the server that resolves the rule's domains, instead of
config.toml's dns list. It is queried the way the rule sends traffic, so a
direct rule's server answers for your own network, not the proxy's:
  celare rules add direct geosite:cn geoip:cn --dns 223.5.5.5`,
	}
	cmd.AddCommand(a.rulesListCmd(), a.rulesAddCmd(), a.rulesRemoveCmd(), a.rulesExplainCmd(), a.rulesGeoUpdateCmd())
	return cmd
}

type ruleView struct {
	Position int `json:"position"`
	config.Rule
}

func ruleSummary(r config.Rule) string {
	s := string(r.Action) + " " + matchText(r)
	if r.DNS != "" {
		s += ", dns " + r.DNS
	}
	if r.Name != "" {
		s = r.Name + ": " + s
	}
	return s
}

// matchText reads as what the rule matches: "geoip:cn", "udp port 443",
// "domain:example.com on port 22".
func matchText(r config.Rule) string {
	s := strings.Join(append(append([]string{}, r.Domains...), r.IPs...), " ")
	var on []string
	if r.Network != "" {
		on = append(on, r.Network)
	}
	if r.Port != "" {
		on = append(on, "port "+r.Port)
	}
	switch {
	case len(on) == 0:
		return s
	case s == "":
		return strings.Join(on, " ")
	default:
		return s + " on " + strings.Join(on, " ")
	}
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
				fmt.Fprintln(tw, "#\tNAME\tACTION\tDNS\tMATCH")
				for _, v := range views {
					fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n", v.Position, dash(v.Name), v.Action, dash(v.DNS), matchText(v.Rule))
				}
				fmt.Fprintf(tw, "-\tdefault\t%s\t-\teverything else\n", def)
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
		case hasDomainPrefix(m):
			domains = append(domains, m)
		case net.ParseIP(m) != nil:
			ips = append(ips, m)
		case strings.Contains(m, "/"):
			if _, _, err := net.ParseCIDR(m); err != nil {
				return nil, nil, fmt.Errorf("%q looks like a CIDR but isn't one", m)
			}
			ips = append(ips, m)
		case strings.Contains(m, ":"):
			return nil, nil, fmt.Errorf("%q has an unknown prefix; use domain:, full:, keyword:, regexp:, geosite: or geoip:, and --port or --network for ports and networks", m)
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
	var name, port, network, dns string
	var first, dryRun bool
	cmd := &cobra.Command{
		Use:   "add <proxy|direct|block> [match...]",
		Short: "Add a rule (last by default, --first to win over the others)",
		Example: `  celare rules add proxy domain:openai.com domain:anthropic.com --name ai --first
  celare rules add direct geosite:cn geoip:cn --dns 223.5.5.5 --name cn
  celare rules add block geosite:category-ads-all
  celare rules add block --network udp --port 443 --name no-quic --first
  celare rules add direct --port 22 --name ssh`,
		Args: minArgs(1, "an action, then matches, --port or --network"),
		RunE: func(cmd *cobra.Command, args []string) error {
			action := config.Action(args[0])
			switch action {
			case config.Proxy, config.Direct, config.Block:
			default:
				return usageError{fmt.Errorf("action %q is not one of proxy, direct, block", args[0])}
			}
			if len(args) == 1 && port == "" && network == "" {
				return usageError{errors.New("give at least one match, --port or --network")}
			}
			domains, ips, err := classify(args[1:])
			if err != nil {
				return usageError{err}
			}
			rule := config.Rule{Name: name, Action: action, Domains: domains, IPs: ips, Network: network, Port: port, DNS: dns}
			if ps := (config.Config{Rules: []config.Rule{rule}}).Check(); len(ps) > 0 {
				return usageError{fmt.Errorf("--%s %s", strings.TrimPrefix(ps[0].Field, "rule[1]."), ps[0].Message)}
			}
			if !dryRun {
				if err := config.AddRule(a.configPath(), rule, first); err != nil {
					return failure("config_edit_failed", err.Error(), "")
				}
			}
			if cfg, err := a.loadConfig(); err == nil {
				switch {
				case dryRun && first:
					cfg.Rules = slices.Insert(cfg.Rules, 0, rule)
				case dryRun:
					cfg.Rules = append(cfg.Rules, rule)
				}
				a.warnDNS(cfg)
			}
			data := map[string]any{"rule": rule, "first": first, "dryRun": dryRun}
			if dryRun {
				return a.ok(data, func(w io.Writer) { fmt.Fprintf(w, "would add rule %s\n", ruleSummary(rule)) })
			}
			return a.ok(data, func(w io.Writer) {
				fmt.Fprintf(w, "added rule %s\n", ruleSummary(rule))
			}, "celare apply --dry-run", "celare apply --confirm 30s")
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "name the rule so it can be removed by name")
	cmd.Flags().StringVar(&port, "port", "", "only traffic to this port, list or range (22, 80,443, 1000-2000)")
	cmd.Flags().StringVar(&network, "network", "", "only tcp or only udp traffic")
	cmd.Flags().StringVar(&dns, "dns", "", "resolve the rule's domains with this server, queried the way the rule routes")
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
	// DNS is the rule whose server looks up a domain target; nil when
	// config.toml's dns list does.
	DNS *ruleView `json:"dns,omitempty"`
}

func (a *app) rulesExplainCmd() *cobra.Command {
	var domain, network string
	cmd := &cobra.Command{
		Use:   "explain <host|ip>[:port]",
		Short: "Show which rule decides where traffic to a target goes",
		Long: `Ask Xray's own router, built from config.toml as it is now, where a
connection through the tunnel would go. Works offline and without the
tunnel.

Xray reads the domain a connection carries (TLS SNI, HTTP Host, QUIC) and
routes by it instead of the dialed address, unless an IP rule names that
address. When no rule names the domain, its addresses are checked against
the IP rules. A domain target is resolved and checked that way. For an IP
target, pass the domain the app sends with --domain.

The port is 443 unless the target names one, and the network is tcp
unless --network says udp. For a domain target, explain also names the
rule whose --dns server looks it up, if one does.`,
		Example: `  celare rules explain openai.com
  celare rules explain github.com:22
  celare rules explain youtube.com --network udp
  celare rules explain 198.41.200.53:7844 --domain h2.cftunnel.com`,
		Args: exactArgs(1, "one target"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			if problems := cfg.Check(); len(problems) > 0 {
				return &apiError{Code: "config_invalid", Message: "config.toml has problems", Problems: problems, Hint: "fix them and run `celare config check`"}
			}
			xray, err := routingConfig(cfg)
			if err != nil {
				return failure("config_invalid", err.Error(), "")
			}
			useGeoDir(a.env.ConfigDir)
			d, err := engine.Explain(xray, engine.Query{Target: args[0], Domain: domain, Network: network})
			if err != nil {
				return failure("explain_failed", err.Error(), "")
			}
			ex := explanation{Decision: d, Action: config.Action(d.Outbound)}
			if i, ok := rulePosition(d.RuleTag, cfg.Rules); ok {
				ex.Rule = &ruleView{Position: i, Rule: cfg.Rules[i-1]}
			}
			if i, ok := rulePosition(d.ResolverTag, cfg.Rules); ok {
				ex.DNS = &ruleView{Position: i, Rule: cfg.Rules[i-1]}
			}
			return a.ok(ex, func(w io.Writer) {
				by := "the default"
				if ex.Rule != nil {
					by = fmt.Sprintf("rule %d (%s)", ex.Rule.Position, ruleSummary(ex.Rule.Rule))
				}
				target := args[0]
				if d.Network == "udp" {
					target += " (udp)"
				}
				if d.RuleTag == "dns-hijack" {
					fmt.Fprintf(w, "%s → celare's DNS, which answers every lookup sent to port 53\n", target)
					return
				}
				fmt.Fprintf(w, "%s → %s, by %s\n", target, ex.Action, by)
				if r := ex.DNS; r != nil {
					fmt.Fprintf(w, "  looked up with %s, rule %d's dns, asked %s\n", r.DNS, r.Position, viaText(r.Action))
				}
				switch {
				case d.RoutedBy == "domain" && len(d.IPs) > 0:
					fmt.Fprintf(w, "  routed by the domain %s, which resolves to %s\n", d.Domain, someOf(d.IPs, 4))
				case d.RoutedBy == "domain":
					fmt.Fprintf(w, "  routed by the domain %s, which doesn't resolve\n", d.Domain)
				case d.RoutedBy == "ip" && d.Domain != "":
					fmt.Fprintf(w, "  routed by the address: a rule names it, so %s doesn't replace it\n", d.Domain)
				case d.SniffOverride:
					fmt.Fprintln(w, "  a connection that carries a domain (TLS, HTTP, QUIC) is routed by that domain instead; check it with --domain")
				}
			})
		},
	}
	cmd.Flags().StringVar(&domain, "domain", "", "the domain the connection carries (TLS SNI, HTTP Host), for an IP target")
	cmd.Flags().StringVar(&network, "network", "tcp", "tcp or udp")
	return cmd
}

func viaText(a config.Action) string {
	if a == config.Proxy {
		return "through the proxy"
	}
	return string(a)
}

func printJSON(w io.Writer, v any) { encodeJSON(w, v) }

// someOf lists up to n of items, and how many more there are.
func someOf(items []string, n int) string {
	if len(items) <= n {
		return strings.Join(items, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(items[:n], ", "), len(items)-n)
}
