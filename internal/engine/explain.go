package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	stdnet "net"
	"slices"
	"strconv"
	"time"

	"github.com/xtls/xray-core/common"
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
	ipTarget := ip != nil
	var resolved []stdnet.IP
	if !ipTarget {
		if sniffed != "" {
			return Decision{}, fmt.Errorf("a sniffed domain only applies to an IP target, not %q", host)
		}
		d.Domain = host
		resolved = lookup(host)
		if len(resolved) > 0 {
			ip = resolved[0]
		}
	}

	facts, err := readFacts(configJSON)
	if err != nil {
		return Decision{}, err
	}
	d.SniffOverride = ip != nil && facts.sniffing && !slices.ContainsFunc(facts.excluded, func(n *stdnet.IPNet) bool { return n.Contains(ip) })

	router, closeFn, err := routerOf(configJSON)
	if err != nil {
		return Decision{}, err
	}
	defer closeFn()

	var route routing.Route
	if d.Domain != "" && (ip == nil || d.SniffOverride) {
		d.RoutedBy = "domain"
		route, err = pickRoute(router, net.DomainAddress(d.Domain), port, nil)
		if err != nil {
			// IPIfNonMatch: no rule matched the domain, so Xray resolves it
			// and tries the IP rules on its addresses.
			if ipTarget {
				resolved = lookup(d.Domain)
			}
			route, err = pickRoute(router, net.DomainAddress(d.Domain), port, resolved)
		}
		for _, ip := range resolved {
			d.IPs = append(d.IPs, ip.String())
		}
	} else {
		d.RoutedBy = "ip"
		route, err = pickRoute(router, net.IPAddress(ip), port, nil)
		d.IPs = []string{ip.String()}
	}
	if errors.Is(err, common.ErrNoClue) {
		// Xray sends what no rule matches to the first outbound.
		d.Outbound, d.RuleTag = facts.firstOutbound, "default"
		return d, nil
	}
	if err != nil {
		return Decision{}, fmt.Errorf("routing %s: %w", target, err)
	}
	d.Outbound, d.RuleTag = route.GetOutboundTag(), route.GetRuleTag()
	return d, nil
}

// withIPs is a routing context whose target resolved to ips.
type withIPs struct {
	routing.Context
	ips []net.IP
}

func (c withIPs) GetTargetIPs() []net.IP { return c.ips }

func pickRoute(router routing.Router, addr net.Address, port int, ips []stdnet.IP) (routing.Route, error) {
	ob := &session.Outbound{Target: net.TCPDestination(addr, net.Port(port))}
	ctx := session.ContextWithInbound(context.Background(), &session.Inbound{Tag: "tun-in"})
	ctx = session.ContextWithOutbounds(ctx, []*session.Outbound{ob})
	// Explain does the lookups itself, so they need no running Xray.
	ctx = session.ContextWithContent(ctx, &session.Content{SkipDNSResolve: true})
	var rctx routing.Context = routingsession.AsRoutingContext(ctx)
	if ips != nil {
		rctx = withIPs{rctx, ips}
	}
	return router.PickRoute(rctx)
}

func lookup(host string) []stdnet.IP {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ips, _ := stdnet.DefaultResolver.LookupIP(ctx, "ip", host)
	return ips
}

type facts struct {
	sniffing      bool
	excluded      []*stdnet.IPNet
	firstOutbound string
}

// readFacts reads what routing depends on beyond Xray's router: the TUN
// inbound's sniffing settings and the first outbound.
func readFacts(configJSON []byte) (facts, error) {
	var cfg struct {
		Inbounds []struct {
			Tag      string `json:"tag"`
			Sniffing struct {
				Enabled     bool     `json:"enabled"`
				IPsExcluded []string `json:"ipsExcluded"`
			} `json:"sniffing"`
		} `json:"inbounds"`
		Outbounds []struct {
			Tag string `json:"tag"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(configJSON, &cfg); err != nil {
		return facts{}, err
	}
	var f facts
	if len(cfg.Outbounds) > 0 {
		f.firstOutbound = cfg.Outbounds[0].Tag
	}
	for _, in := range cfg.Inbounds {
		if in.Tag != "tun-in" {
			continue
		}
		f.sniffing = in.Sniffing.Enabled
		for _, m := range in.Sniffing.IPsExcluded {
			if _, n, err := stdnet.ParseCIDR(m); err == nil {
				f.excluded = append(f.excluded, n)
			} else if ip := stdnet.ParseIP(m); ip != nil {
				f.excluded = append(f.excluded, &stdnet.IPNet{IP: ip, Mask: stdnet.CIDRMask(len(ip)*8, len(ip)*8)})
			}
		}
	}
	return f, nil
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
