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
			{Action: config.Direct, IPs: []string{"10.0.0.0/8"}},
		},
		TUN: &xrayconf.TUN{Name: "utun99", MTU: 1500},
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		target, outbound, rule string
	}{
		{"tracker.ads.example.test", "block", "rule-1"},
		{"10.1.2.3", "direct", "rule-2"},
		{"10.1.2.3:8443", "direct", "rule-2"},
		{"203.0.113.77", "proxy", "default"},
		{"site.example.test", "proxy", "default"},
	}
	for _, tc := range cases {
		got, err := engine.Explain(cfg, tc.target)
		if err != nil {
			t.Fatalf("Explain(%q): %v", tc.target, err)
		}
		if got.Outbound != tc.outbound || got.RuleTag != tc.rule {
			t.Errorf("Explain(%q) = %s via %s, want %s via %s", tc.target, got.Outbound, got.RuleTag, tc.outbound, tc.rule)
		}
	}
}
