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
// 1.1.1.1 is on some networks.
func (a *app) dnsWarnings(cfg config.Config) []string {
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
		return nil
	}
	useGeoDir(a.env.ConfigDir)
	var out []string
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
		r := cfg.Rules[pos-1]
		label := fmt.Sprintf("rule %d", pos)
		if r.Name != "" {
			label += fmt.Sprintf(" (%s)", r.Name)
		}
		fix := fmt.Sprintf("celare rules add %s %s --name dns --first", def, ip)
		if action == config.Block {
			out = append(out, fmt.Sprintf("%s blocks your DNS server %s, so nothing will load; to let lookups through: %s", label, ip, fix))
		} else {
			out = append(out, fmt.Sprintf("%s sends your DNS server %s direct, outside the VPN; if %s is blocked there, nothing will load; to keep lookups on the VPN: %s", label, ip, ip, fix))
		}
	}
	return out
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
