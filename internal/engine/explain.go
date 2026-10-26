package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	stdnet "net"
	"slices"
	"strconv"
	"time"

	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/routing"
	routingsession "github.com/xtls/xray-core/features/routing/session"
)

// Decision is where Xray would send a connection.
type Decision struct {
	Target string   `json:"target"`
	Domain string   `json:"domain,omitempty"`
	IPs    []string `json:"ips,omitempty"`
	Port   int      `json:"port"`
	// RoutedBy is what the router matched on: "ip" or "domain".
	RoutedBy string `json:"routedBy"`
	// SniffOverride says a domain sniffed from the connection (TLS SNI,
	// HTTP Host, QUIC) would replace the address, so the domain decides.
	SniffOverride bool   `json:"sniffOverride"`
	Outbound      string `json:"outbound"`
	RuleTag       string `json:"ruleTag"`
}

// Explain asks Xray's router, built from configJSON but not started,
// where a TUN connection to target ("host", "host:port", "ip") would go.
// sniffed is the domain the connection carries, for an IP target. A
// domain target stands for an app that resolves it and sends it as SNI,
// so it is resolved and then treated the same way.
func Explain(configJSON []byte, target, sniffed string) (Decision, error) {
	host, port := target, 443
	if h, p, err := stdnet.SplitHostPort(target); err == nil {
		n, err := strconv.Atoi(p)
		if err != nil {
			return Decision{}, fmt.Errorf("target %q has an invalid port", target)
		}
		host, port = h, n
	}
	d := Decision{Target: target, Port: port, Domain: sniffed}

	ip := stdnet.ParseIP(host)
	if ip == nil {
		if sniffed != "" {
			return Decision{}, fmt.Errorf("a sniffed domain only applies to an IP target, not %q", host)
		}
		d.Domain = host
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		ips, _ := stdnet.DefaultResolver.LookupIP(ctx, "ip", host)
		cancel()
		for _, ip := range ips {
			d.IPs = append(d.IPs, ip.String())
		}
		if len(ips) > 0 {
			ip = ips[0]
		}
	} else {
		d.IPs = []string{ip.String()}
	}

	enabled, excluded, err := tunSniffing(configJSON)
	if err != nil {
		return Decision{}, err
	}
	d.SniffOverride = ip != nil && enabled && !slices.ContainsFunc(excluded, func(n *stdnet.IPNet) bool { return n.Contains(ip) })

	ob := &session.Outbound{}
	if d.Domain != "" && (ip == nil || d.SniffOverride) {
		d.RoutedBy = "domain"
		ob.Target = net.TCPDestination(net.DomainAddress(d.Domain), net.Port(port))
	} else {
		d.RoutedBy = "ip"
		ob.Target = net.TCPDestination(net.IPAddress(ip), net.Port(port))
	}

	router, closeFn, err := routerOf(configJSON)
	if err != nil {
		return Decision{}, err
	}
	defer closeFn()

	ctx := session.ContextWithInbound(context.Background(), &session.Inbound{Tag: "tun-in"})
	ctx = session.ContextWithOutbounds(ctx, []*session.Outbound{ob})
	// No lookups: celare's routing ends in a catch-all rule, so Xray's
	// IPIfNonMatch never resolves a domain to try IP rules.
	ctx = session.ContextWithContent(ctx, &session.Content{SkipDNSResolve: true})
	route, err := router.PickRoute(routingsession.AsRoutingContext(ctx))
	if err != nil {
		return Decision{}, fmt.Errorf("no route matched %s: %w", target, err)
	}
	d.Outbound, d.RuleTag = route.GetOutboundTag(), route.GetRuleTag()
	return d, nil
}

// tunSniffing reads the TUN inbound's sniffing settings from configJSON.
func tunSniffing(configJSON []byte) (enabled bool, excluded []*stdnet.IPNet, err error) {
	var cfg struct {
		Inbounds []struct {
			Tag      string `json:"tag"`
			Sniffing struct {
				Enabled     bool     `json:"enabled"`
				IPsExcluded []string `json:"ipsExcluded"`
			} `json:"sniffing"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(configJSON, &cfg); err != nil {
		return false, nil, err
	}
	for _, in := range cfg.Inbounds {
		if in.Tag != "tun-in" {
			continue
		}
		for _, m := range in.Sniffing.IPsExcluded {
			if _, n, err := stdnet.ParseCIDR(m); err == nil {
				excluded = append(excluded, n)
			} else if ip := stdnet.ParseIP(m); ip != nil {
				excluded = append(excluded, &stdnet.IPNet{IP: ip, Mask: stdnet.CIDRMask(len(ip)*8, len(ip)*8)})
			}
		}
		return in.Sniffing.Enabled, excluded, nil
	}
	return false, nil, nil
}

// routerOf builds an Xray instance without inbounds, so nothing listens
// and no interface is created, and returns its router.
func routerOf(configJSON []byte) (routing.Router, func(), error) {
	var raw map[string]any
	if err := json.Unmarshal(configJSON, &raw); err != nil {
		return nil, nil, err
	}
	delete(raw, "inbounds")
	raw["log"] = map[string]string{"loglevel": "none"}
	stripped, _ := json.Marshal(raw)
	cfg, err := core.LoadConfig("json", bytes.NewReader(stripped))
	if err != nil {
		return nil, nil, fmt.Errorf("xray rejected the config: %w", err)
	}
	instance, err := core.New(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("xray could not build the config: %w", err)
	}
	router, ok := instance.GetFeature(routing.RouterType()).(routing.Router)
	if !ok {
		instance.Close()
		return nil, nil, fmt.Errorf("xray built no router")
	}
	return router, func() { instance.Close() }, nil
}
