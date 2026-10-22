package xrayconf

import (
	"encoding/json"
	"fmt"
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
	Servers []string `json:"servers"`
	Tag     string   `json:"tag"`
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

var defaultSniffing = sniffing{Enabled: true, DestOverride: []string{"http", "tls", "quic"}}

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
		Routing: routing{DomainStrategy: "IPIfNonMatch"},
	}
	if len(p.DNS) > 0 {
		cfg.DNS = &dnsConfig{Servers: p.DNS, Tag: "dns-internal"}
	}
	if p.TUN != nil {
		cfg.Inbounds = append(cfg.Inbounds, inbound{
			Tag:      tagTUN,
			Protocol: "tun",
			Settings: map[string]any{"name": p.TUN.Name, "mtu": p.TUN.MTU},
			Sniffing: defaultSniffing,
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
			Sniffing: defaultSniffing,
		})
	}
	for i, r := range p.Rules {
		tag, err := outboundFor(r.Action)
		if err != nil {
			return nil, fmt.Errorf("rule %d: %w", i+1, err)
		}
		ruleTag := "rule-" + strconv.Itoa(i+1)
		if len(r.Domains) > 0 {
			cfg.Routing.Rules = append(cfg.Routing.Rules, routingRule{RuleTag: ruleTag, Domain: r.Domains, OutboundTag: tag})
		}
		if len(r.IPs) > 0 {
			cfg.Routing.Rules = append(cfg.Routing.Rules, routingRule{RuleTag: ruleTag, IP: r.IPs, OutboundTag: tag})
		}
	}
	def, err := outboundFor(p.Default)
	if err != nil {
		return nil, fmt.Errorf("default route: %w", err)
	}
	cfg.Routing.Rules = append(cfg.Routing.Rules, routingRule{RuleTag: "default", Network: "tcp,udp", OutboundTag: def})
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
