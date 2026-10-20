package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	stdnet "net"
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
	Target   string   `json:"target"`
	Domain   string   `json:"domain,omitempty"`
	IPs      []string `json:"ips,omitempty"`
	Port     int      `json:"port"`
	Outbound string   `json:"outbound"`
	RuleTag  string   `json:"ruleTag"`
}

// Explain asks Xray's router, built from configJSON but not started,
// where a connection to target ("host", "host:port", "ip") would go. A
// domain is also resolved, so IP rules are checked the way they are for
// sniffed TUN traffic, which carries both.
func Explain(configJSON []byte, target string) (Decision, error) {
	host, port := target, 443
	if h, p, err := stdnet.SplitHostPort(target); err == nil {
		n, err := strconv.Atoi(p)
		if err != nil {
			return Decision{}, fmt.Errorf("target %q has an invalid port", target)
		}
		host, port = h, n
	}
	d := Decision{Target: target, Port: port}

	ob := &session.Outbound{}
	if ip := stdnet.ParseIP(host); ip != nil {
		d.IPs = []string{ip.String()}
		ob.Target = net.TCPDestination(net.IPAddress(ip), net.Port(port))
	} else {
		d.Domain = host
		domainDest := net.TCPDestination(net.DomainAddress(host), net.Port(port))
		ob.Target, ob.RouteTarget = domainDest, domainDest
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		ips, _ := stdnet.DefaultResolver.LookupIP(ctx, "ip", host)
		cancel()
		if len(ips) > 0 {
			ob.Target = net.TCPDestination(net.IPAddress(ips[0]), net.Port(port))
			for _, ip := range ips {
				d.IPs = append(d.IPs, ip.String())
			}
		}
	}

	router, closeFn, err := routerOf(configJSON)
	if err != nil {
		return Decision{}, err
	}
	defer closeFn()

	ctx := session.ContextWithInbound(context.Background(), &session.Inbound{Tag: "tun-in"})
	ctx = session.ContextWithOutbounds(ctx, []*session.Outbound{ob})
	ctx = session.ContextWithContent(ctx, &session.Content{SkipDNSResolve: true})
	route, err := router.PickRoute(routingsession.AsRoutingContext(ctx))
	if err != nil {
		return Decision{}, fmt.Errorf("no route matched %s: %w", target, err)
	}
	d.Outbound, d.RuleTag = route.GetOutboundTag(), route.GetRuleTag()
	return d, nil
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
