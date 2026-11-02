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
	// Network is "tcp" or "udp".
	Network string `json:"network"`
	// RoutedBy is what the router matched on: "ip" or "domain".
	RoutedBy string `json:"routedBy"`
	// SniffOverride says a domain sniffed from the connection (TLS SNI,
	// HTTP Host, QUIC) would replace the address, so the domain decides.
	SniffOverride bool   `json:"sniffOverride"`
	Outbound      string `json:"outbound"`
	RuleTag       string `json:"ruleTag"`
	// ResolverTag is the tag of the DNS server Xray asks first for a
	// domain target, when a server that names domains matches it.
	ResolverTag string `json:"resolverTag,omitempty"`
}

// Query is a connection to explain.
type Query struct {
	// Target is "host", "host:port" or "ip"; the port is 443 unless named.
	Target string
	// Domain is the domain the connection carries (TLS SNI), for an IP target.
	Domain string
	// Network is "tcp" or "udp"; "" is tcp.
	Network string
	// FromDNS makes it a query from Xray's own DNS client, which reaches
	// the router from the dns tag instead of the TUN, without sniffing.
	FromDNS bool
}

// Explain asks Xray's router, built from configJSON but not started,
// where a connection would go. A domain target stands for an app that
// resolves it and sends it as SNI, so it is resolved and then treated
// the same way.
func Explain(configJSON []byte, q Query) (Decision, error) {
	target, sniffed, network := q.Target, q.Domain, q.Network
	host, port := target, 443
	if h, p, err := stdnet.SplitHostPort(target); err == nil {
		n, err := strconv.Atoi(p)
		if err != nil {
			return Decision{}, fmt.Errorf("target %q has an invalid port", target)
		}
		host, port = h, n
	}
	switch network {
	case "":
		network = "tcp"
	case "tcp", "udp":
	default:
		return Decision{}, fmt.Errorf("network %q is not tcp or udp", network)
	}
	d := Decision{Target: target, Port: port, Network: network, Domain: sniffed}

	ip := stdnet.ParseIP(host)
	ipTarget := ip != nil
	var resolved []stdnet.IP
	var err error
	if !ipTarget {
		if sniffed != "" {
			return Decision{}, fmt.Errorf("a sniffed domain only applies to an IP target, not %q", host)
		}
		d.Domain = host
		resolved = lookup(host)
		if len(resolved) > 0 {
			ip = resolved[0]
		}
		if d.ResolverTag, err = resolverFor(configJSON, host); err != nil {
			return Decision{}, err
		}
	}

	facts, err := readFacts(configJSON)
	if err != nil {
		return Decision{}, err
	}
	inbound := "tun-in"
	if q.FromDNS {
		inbound, facts.sniffing = facts.dnsTag, false
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
		route, err = pickRoute(router, inbound, dest(net.DomainAddress(d.Domain), port, network), nil)
		if err != nil {
			// IPIfNonMatch: no rule matched the domain, so Xray resolves it
			// and tries the IP rules on its addresses.
			if ipTarget {
				resolved = lookup(d.Domain)
			}
			route, err = pickRoute(router, inbound, dest(net.DomainAddress(d.Domain), port, network), resolved)
		}
		for _, ip := range resolved {
			d.IPs = append(d.IPs, ip.String())
		}
	} else {
		d.RoutedBy = "ip"
		route, err = pickRoute(router, inbound, dest(net.IPAddress(ip), port, network), nil)
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

// resolverFor is the tag of the DNS server Xray asks first for domain,
// among the servers that name domains, or "" when none matches. Xray asks
// the first such server in config order, and its router matches domains
// the same way, so a router with one rule per server finds it.
func resolverFor(configJSON []byte, domain string) (string, error) {
	var cfg struct {
		DNS struct {
			Servers []json.RawMessage `json:"servers"`
		} `json:"dns"`
	}
	if err := json.Unmarshal(configJSON, &cfg); err != nil {
		return "", err
	}
	var rules []map[string]any
	for _, raw := range cfg.DNS.Servers {
		var s struct {
			Domains []string `json:"domains"`
			Tag     string   `json:"tag"`
		}
		// Plain servers are strings and name no domains.
		if json.Unmarshal(raw, &s) != nil || len(s.Domains) == 0 || s.Tag == "" {
			continue
		}
		rules = append(rules, map[string]any{"ruleTag": s.Tag, "domain": s.Domains, "outboundTag": "x"})
	}
	if len(rules) == 0 {
		return "", nil
	}
	probe, _ := json.Marshal(map[string]any{
		"outbounds": []map[string]string{{"tag": "x", "protocol": "freedom"}},
		"routing":   map[string]any{"rules": rules},
	})
	router, closeFn, err := routerOf(probe)
	if err != nil {
		return "", err
	}
	defer closeFn()
	route, err := pickRoute(router, "", net.TCPDestination(net.DomainAddress(domain), 443), nil)
	if errors.Is(err, common.ErrNoClue) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return route.GetRuleTag(), nil
}

// withIPs is a routing context whose target resolved to ips.
type withIPs struct {
	routing.Context
	ips []net.IP
}

func (c withIPs) GetTargetIPs() []net.IP { return c.ips }

func dest(addr net.Address, port int, network string) net.Destination {
	if network == "udp" {
		return net.UDPDestination(addr, net.Port(port))
	}
	return net.TCPDestination(addr, net.Port(port))
}

func pickRoute(router routing.Router, inbound string, target net.Destination, ips []stdnet.IP) (routing.Route, error) {
	ob := &session.Outbound{Target: target}
	ctx := session.ContextWithInbound(context.Background(), &session.Inbound{Tag: inbound})
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
	dnsTag        string
	sniffing      bool
	excluded      []*stdnet.IPNet
	firstOutbound string
}

// readFacts reads what routing depends on beyond Xray's router: the TUN
// inbound's sniffing settings, the DNS client's tag and the first outbound.
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
		DNS struct {
			Tag string `json:"tag"`
		} `json:"dns"`
	}
	if err := json.Unmarshal(configJSON, &cfg); err != nil {
		return facts{}, err
	}
	f := facts{dnsTag: cfg.DNS.Tag}
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
