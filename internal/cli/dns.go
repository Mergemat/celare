package cli

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/Mergemat/celare/internal/config"
	"github.com/Mergemat/celare/internal/engine"
	"github.com/Mergemat/celare/internal/node"
	"github.com/Mergemat/celare/internal/xrayconf"
)

// routingConfig renders the Xray config that routes like cfg would.
func routingConfig(cfg config.Config) ([]byte, error) {
	def := cfg.Default
	if def == "" {
		def = config.Proxy
	}
	return xrayconf.Config(xrayconf.Plan{
		// Routing doesn't depend on the node; any valid one will do.
		Node:    node.Node{Protocol: "trojan", Address: "127.0.0.1", Port: 1, Password: "x"},
		Default: def,
		Rules:   cfg.Rules,
		TUN:     &xrayconf.TUN{Name: "utun99", MTU: 1500},
	})
}

// dnsWarnings names the rules that send a DNS server direct, or block
// it, when the default would carry it through the proxy. Lookups follow
// the rules, so they then fail wherever that server is unreachable, as
// 1.1.1.1 is on some networks. fix is a command that undoes this, or "".
func (a *app) dnsWarnings(cfg config.Config) (warnings []string, fix string) {
	def := cfg.Default
	if def == "" {
		def = config.Proxy
	}
	servers := cfg.DNS
	if len(servers) == 0 {
		servers = config.DefaultDNS
	}
	xray, err := routingConfig(cfg)
	if err != nil {
		return nil, ""
	}
	useGeoDir(a.env.ConfigDir)
	var caught []string
	for _, s := range servers {
		ip := dnsServerIP(s)
		// A LAN server is only reachable directly.
		if ip == nil || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		d, err := engine.Explain(xray, ip.String(), "")
		if err != nil {
			continue
		}
		pos, ok := rulePosition(d.RuleTag, cfg.Rules)
		if !ok {
			continue
		}
		action := config.Action(d.Outbound)
		if action == config.Proxy || (action == config.Direct && def == config.Direct) {
			continue
		}
		label := fmt.Sprintf("rule %d", pos)
		if name := cfg.Rules[pos-1].Name; name != "" {
			label += fmt.Sprintf(" (%s)", name)
		}
		if action == config.Block {
			warnings = append(warnings, fmt.Sprintf("%s blocks your DNS server %s, so nothing will load", label, ip))
		} else {
			warnings = append(warnings, fmt.Sprintf("%s sends your DNS server %s direct, outside the VPN; if it's blocked there, nothing will load", label, ip))
		}
		caught = append(caught, ip.String())
	}
	if len(caught) > 0 {
		fix = fmt.Sprintf("celare rules add %s %s --name dns --first", def, strings.Join(caught, " "))
	}
	return warnings, fix
}

// warnDNS queues cfg's DNS warnings, and their fix, for the next result.
func (a *app) warnDNS(cfg config.Config) {
	warnings, fix := a.dnsWarnings(cfg)
	a.warnings = append(a.warnings, warnings...)
	if fix != "" {
		a.fixes = append(a.fixes, fix)
	}
}

// dnsServerIP is the address Xray dials for a DNS server, or nil when it
// names a host or bypasses routing ("+local").
func dnsServerIP(server string) net.IP {
	host := server
	if strings.Contains(server, "://") {
		u, err := url.Parse(server)
		if err != nil || strings.HasSuffix(u.Scheme, "+local") {
			return nil
		}
		host = u.Hostname()
	} else if h, _, err := net.SplitHostPort(server); err == nil {
		host = h
	}
	return net.ParseIP(host)
}

// rulePosition turns a rule tag ("rule-3") into a position in rules.
func rulePosition(tag string, rules []config.Rule) (int, bool) {
	n, ok := strings.CutPrefix(tag, "rule-")
	if !ok {
		return 0, false
	}
	var pos int
	if _, err := fmt.Sscan(n, &pos); err != nil || pos < 1 || pos > len(rules) {
		return 0, false
	}
	return pos, true
}
