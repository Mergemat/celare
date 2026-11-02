package xrayconf

import (
	"encoding/json"
	"fmt"
	"net"
	"slices"
	"strconv"

	"github.com/Mergemat/celare/internal/config"
	"github.com/Mergemat/celare/internal/node"
)

// Plan is everything needed to render one complete Xray config.
type Plan struct {
	Node    node.Node
	Default config.Action
	Rules   []config.Rule
	DNS     []string
	TUN     *TUN
	// SOCKSPort, when set, adds a loopback SOCKS inbound (tests, diagnostics).
	SOCKSPort int
	// BindInterface is the physical interface outbound traffic must use, so
	// Xray's own connections never loop back into the tunnel.
	BindInterface string
	// LogLevel is Xray's log level; "" means warning. Xray logs to
	// stdout, so commands printing JSON there use "none".
	LogLevel string
	// LogFile, if set, receives Xray's log instead of stdout; per-connection
	// access lines are then left out.
	LogFile string
}

type TUN struct {
	Name string
	MTU  int
}

// Outbound tags. Rule actions map onto the first three by name.
const (
	tagProxy  = "proxy"
	tagDirect = "direct"
	tagBlock  = "block"
	tagDNS    = "dns-out"
	tagTUN    = "tun-in"
	tagSOCKS  = "socks-in"
)

type xrayConfig struct {
	Log       map[string]string `json:"log"`
	DNS       *dnsConfig        `json:"dns,omitempty"`
	Inbounds  []inbound         `json:"inbounds"`
	Outbounds []OutboundObject  `json:"outbounds"`
	Routing   routing           `json:"routing"`
}

type dnsConfig struct {
	// Servers holds dnsServer objects, then the plain dns list.
	Servers []any  `json:"servers"`
	Tag     string `json:"tag"`
}

// dnsServer resolves one rule's domains. SkipFallback keeps it from
// answering for any other domain, which would leak those lookups to it.
type dnsServer struct {
	Address      string   `json:"address"`
	Domains      []string `json:"domains"`
	SkipFallback bool     `json:"skipFallback"`
	// Tag is the inbound its queries are routed from.
	Tag string `json:"tag"`
}

type inbound struct {
	Tag      string         `json:"tag"`
	Protocol string         `json:"protocol"`
	Listen   string         `json:"listen,omitempty"`
	Port     int            `json:"port,omitempty"`
	Settings map[string]any `json:"settings"`
	Sniffing sniffing       `json:"sniffing"`
}

type sniffing struct {
	Enabled      bool     `json:"enabled"`
	DestOverride []string `json:"destOverride"`
	// RouteOnly false sends the sniffed domain to the server instead of
	// the IP the app dialed: an app that looked it up through another VPN
	// (V2Box) holds a fake 198.18.x address only that VPN understands.
	RouteOnly bool `json:"routeOnly"`
	// IPsExcluded keeps the dialed address for the IPs and CIDRs rules
	// name: those are real addresses, and an IP rule can only match the
	// address, never a sniffed domain (h2.cftunnel.com on Cloudflare's
	// tunnel edge resolves to nothing).
	IPsExcluded []string `json:"ipsExcluded,omitempty"`
}

type routing struct {
	DomainStrategy string        `json:"domainStrategy"`
	Rules          []routingRule `json:"rules"`
}

type routingRule struct {
	RuleTag     string   `json:"ruleTag"`
	InboundTag  []string `json:"inboundTag,omitempty"`
	Domain      []string `json:"domain,omitempty"`
	IP          []string `json:"ip,omitempty"`
	Port        string   `json:"port,omitempty"`
	Network     string   `json:"network,omitempty"`
	OutboundTag string   `json:"outboundTag"`
}

func sniffingFor(rules []config.Rule) sniffing {
	s := sniffing{Enabled: true, DestOverride: []string{"http", "tls", "quic"}}
	for _, r := range rules {
		for _, m := range r.IPs {
			if _, _, err := net.ParseCIDR(m); err == nil || net.ParseIP(m) != nil {
				s.IPsExcluded = append(s.IPsExcluded, m)
			}
		}
	}
	return s
}

// Config renders a complete Xray JSON config for the plan.
func Config(p Plan) ([]byte, error) {
	proxy, err := Outbound(p.Node, tagProxy)
	if err != nil {
		return nil, err
	}
	direct := OutboundObject{Tag: tagDirect, Protocol: "freedom"}
	if p.BindInterface != "" {
		for _, o := range []*OutboundObject{&proxy, &direct} {
			if o.StreamSettings == nil {
				o.StreamSettings = &StreamSettings{}
			}
			o.StreamSettings.Sockopt = &Sockopt{Interface: p.BindInterface}
		}
	}
	logLevel := p.LogLevel
	if logLevel == "" {
		logLevel = "warning"
	}
	logCfg := map[string]string{"loglevel": logLevel}
	if p.LogFile != "" {
		logCfg["error"] = p.LogFile
		logCfg["access"] = "none"
	}
	cfg := xrayConfig{
		Log: logCfg,
		Outbounds: []OutboundObject{
			proxy,
			direct,
			{Tag: tagBlock, Protocol: "blackhole"},
			{Tag: tagDNS, Protocol: "dns"},
		},
		Routing: routing{DomainStrategy: "AsIs"},
	}
	// IPIfNonMatch resolves a domain no rule matched, to try the IP rules
	// on its addresses. Without IP rules the lookup only costs time, and
	// node tests would depend on the local resolver.
	if slices.ContainsFunc(p.Rules, func(r config.Rule) bool { return len(r.IPs) > 0 }) {
		cfg.Routing.DomainStrategy = "IPIfNonMatch"
	}
	sniff := sniffingFor(p.Rules)
	var dnsRules []routingRule
	var servers []any
	for i, r := range p.Rules {
		if r.DNS == "" {
			continue
		}
		tag, err := outboundFor(r.Action)
		if err != nil {
			return nil, fmt.Errorf("rule %d: %w", i+1, err)
		}
		// The server is asked the way the rule sends its traffic, so it
		// answers for the network that traffic leaves from. This rule
		// precedes the user's, which would otherwise route its queries
		// by the server's address.
		dnsTag := "rule-" + strconv.Itoa(i+1) + "-dns"
		servers = append(servers, dnsServer{Address: r.DNS, Domains: r.Domains, SkipFallback: true, Tag: dnsTag})
		dnsRules = append(dnsRules, routingRule{RuleTag: dnsTag, InboundTag: []string{dnsTag}, OutboundTag: tag})
	}
	for _, s := range p.DNS {
		servers = append(servers, s)
	}
	if len(servers) > 0 {
		cfg.DNS = &dnsConfig{Servers: servers, Tag: "dns-internal"}
	}
	if p.TUN != nil {
		cfg.Inbounds = append(cfg.Inbounds, inbound{
			Tag:      tagTUN,
			Protocol: "tun",
			Settings: map[string]any{"name": p.TUN.Name, "mtu": p.TUN.MTU},
			Sniffing: sniff,
		})
		cfg.Routing.Rules = append(cfg.Routing.Rules, routingRule{
			RuleTag: "dns-hijack", InboundTag: []string{tagTUN}, Port: "53", OutboundTag: tagDNS,
		})
	}
	if p.SOCKSPort != 0 {
		cfg.Inbounds = append(cfg.Inbounds, inbound{
			Tag:      tagSOCKS,
			Protocol: "socks",
			Listen:   "127.0.0.1",
			Port:     p.SOCKSPort,
			Settings: map[string]any{"udp": true},
			Sniffing: sniff,
		})
	}
	cfg.Routing.Rules = append(cfg.Routing.Rules, dnsRules...)
	for i, r := range p.Rules {
		tag, err := outboundFor(r.Action)
		if err != nil {
			return nil, fmt.Errorf("rule %d: %w", i+1, err)
		}
		// Xray ANDs the conditions of one rule, and celare ORs domains
		// with IPs, so each becomes its own Xray rule. Port and network
		// narrow both.
		base := routingRule{Port: r.Port, Network: r.Network, OutboundTag: tag}
		var parts []routingRule
		if len(r.Domains) > 0 {
			part := base
			part.Domain = r.Domains
			parts = append(parts, part)
		}
		if len(r.IPs) > 0 {
			part := base
			part.IP = r.IPs
			parts = append(parts, part)
		}
		if len(parts) == 0 {
			parts = append(parts, base)
		}
		for j, part := range parts {
			// Xray rejects a repeated ruleTag; rules explain maps both back.
			part.RuleTag = "rule-" + strconv.Itoa(i+1)
			if j > 0 {
				part.RuleTag += "-ip"
			}
			cfg.Routing.Rules = append(cfg.Routing.Rules, part)
		}
	}
	def, err := outboundFor(p.Default)
	if err != nil {
		return nil, fmt.Errorf("default route: %w", err)
	}
	// Xray sends what no rule matches to the first outbound. A catch-all
	// rule instead would stop IPIfNonMatch from ever resolving a domain to
	// try the IP rules, so geoip: rules would never see domain traffic.
	i := slices.IndexFunc(cfg.Outbounds, func(o OutboundObject) bool { return o.Tag == def })
	cfg.Outbounds[0], cfg.Outbounds[i] = cfg.Outbounds[i], cfg.Outbounds[0]
	return json.MarshalIndent(cfg, "", "  ")
}

func outboundFor(a config.Action) (string, error) {
	switch a {
	case config.Proxy, config.Direct, config.Block:
		return string(a), nil
	default:
		return "", fmt.Errorf("action %q is not one of proxy, direct, block", a)
	}
}
