package engine_test

import (
	"testing"

	"github.com/Mergemat/celare/internal/config"
	"github.com/Mergemat/celare/internal/engine"
	"github.com/Mergemat/celare/internal/node"
	"github.com/Mergemat/celare/internal/xrayconf"
)

func TestExplainUsesXraysOwnRouter(t *testing.T) {
	cfg, err := xrayconf.Config(xrayconf.Plan{
		Node:    node.Node{Protocol: "trojan", Address: "203.0.113.1", Port: 443, Password: "pw"},
		Default: config.Proxy,
		Rules: []config.Rule{
			{Action: config.Block, Domains: []string{"domain:ads.example.test"}},
			{Action: config.Direct, IPs: []string{"10.0.0.0/8", "2001:db8::/32"}},
			{Action: config.Direct, IPs: []string{"127.0.0.0/8", "::1/128"}},
		},
		TUN: &xrayconf.TUN{Name: "utun99", MTU: 1500},
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		target, domain, outbound, rule, routedBy string
	}{
		{"tracker.ads.example.test", "", "block", "rule-1", "domain"},
		{"10.1.2.3", "", "direct", "rule-2", "ip"},
		{"10.1.2.3:8443", "", "direct", "rule-2", "ip"},
		{"[2001:db8::1]:7844", "", "direct", "rule-2", "ip"},
		{"203.0.113.77", "", "proxy", "default", "ip"},
		{"site.example.test", "", "proxy", "default", "domain"},
		// The address a rule names is what the app dialed; a sniffed
		// domain must not take its place, even one nothing resolves.
		{"10.1.2.3:7844", "h2.unresolvable.example.test", "direct", "rule-2", "ip"},
		// Anywhere else, Xray routes by the sniffed domain.
		{"203.0.113.77", "tracker.ads.example.test", "block", "rule-1", "domain"},
		// No rule names the domain, so Xray resolves it and tries the IP
		// rules on its addresses.
		{"203.0.113.77", "localhost", "direct", "rule-3", "domain"},
	}
	for _, tc := range cases {
		got, err := engine.Explain(cfg, tc.target, tc.domain)
		if err != nil {
			t.Fatalf("Explain(%q, %q): %v", tc.target, tc.domain, err)
		}
		if got.Outbound != tc.outbound || got.RuleTag != tc.rule || got.RoutedBy != tc.routedBy {
			t.Errorf("Explain(%q, %q) = %s via %s by %s, want %s via %s by %s", tc.target, tc.domain,
				got.Outbound, got.RuleTag, got.RoutedBy, tc.outbound, tc.rule, tc.routedBy)
		}
	}
}

func TestExplainSaysWhenASniffedDomainWouldDecide(t *testing.T) {
	cfg, err := xrayconf.Config(xrayconf.Plan{
		Node:    node.Node{Protocol: "trojan", Address: "203.0.113.1", Port: 443, Password: "pw"},
		Default: config.Proxy,
		Rules:   []config.Rule{{Action: config.Direct, IPs: []string{"10.0.0.0/8"}}},
		TUN:     &xrayconf.TUN{Name: "utun99", MTU: 1500},
	})
	if err != nil {
		t.Fatal(err)
	}
	for target, want := range map[string]bool{"10.1.2.3": false, "203.0.113.77": true} {
		got, err := engine.Explain(cfg, target, "")
		if err != nil {
			t.Fatal(err)
		}
		if got.SniffOverride != want {
			t.Errorf("Explain(%q).SniffOverride = %v, want %v", target, got.SniffOverride, want)
		}
	}
}
