package xrayconf_test

import (
	"bytes"
	"testing"

	"github.com/b-omarov/celare/internal/config"
	"github.com/b-omarov/celare/internal/node"
	"github.com/b-omarov/celare/internal/xrayconf"
	"github.com/google/go-cmp/cmp"
	"github.com/xtls/xray-core/core"
)

var plainVLESS = node.Node{
	Protocol:   "vless",
	Address:    "203.0.113.5",
	Port:       443,
	UUID:       "22222222-3333-4444-9555-666666666666",
	Encryption: "none",
}

// A rule's domain and IP conditions become separate Xray rules, because
// Xray ANDs conditions within one rule and celare ORs them. Every
// outbound that leaves the machine is bound to the physical interface so
// it never loops back into the tunnel.
func TestConfigRoutingAndInterfaceBinding(t *testing.T) {
	got := renderConfig(t, xrayconf.Plan{
		Node:    plainVLESS,
		Default: config.Proxy,
		Rules: []config.Rule{
			{Action: config.Direct, Domains: []string{"full:localhost"}, IPs: []string{"192.168.0.0/16"}},
			{Action: config.Block, IPs: []string{"::1/128"}},
		},
		DNS:           []string{"1.1.1.1"},
		SOCKSPort:     10808,
		BindInterface: "en0",
	})
	want := `{
		"log": {"loglevel": "warning"},
		"dns": {"servers": ["1.1.1.1"], "tag": "dns-internal"},
		"inbounds": [{
			"tag": "socks-in",
			"protocol": "socks",
			"listen": "127.0.0.1",
			"port": 10808,
			"settings": {"udp": true},
			"sniffing": {"enabled": true, "destOverride": ["http", "tls", "quic"], "routeOnly": true}
		}],
		"outbounds": [
			{
				"tag": "proxy",
				"protocol": "vless",
				"settings": {"vnext": [{"address": "203.0.113.5", "port": 443, "users": [{"id": "22222222-3333-4444-9555-666666666666", "encryption": "none"}]}]},
				"streamSettings": {"network": "raw", "sockopt": {"interface": "en0"}}
			},
			{"tag": "direct", "protocol": "freedom", "streamSettings": {"sockopt": {"interface": "en0"}}},
			{"tag": "block", "protocol": "blackhole"},
			{"tag": "dns-out", "protocol": "dns"}
		],
		"routing": {
			"domainStrategy": "IPIfNonMatch",
			"rules": [
				{"ruleTag": "rule-1", "domain": ["full:localhost"], "outboundTag": "direct"},
				{"ruleTag": "rule-1", "ip": ["192.168.0.0/16"], "outboundTag": "direct"},
				{"ruleTag": "rule-2", "ip": ["::1/128"], "outboundTag": "block"},
				{"ruleTag": "default", "network": "tcp,udp", "outboundTag": "proxy"}
			]
		}
	}`
	if diff := cmp.Diff(decode(t, []byte(want)), decode(t, got)); diff != "" {
		t.Errorf("config mismatch (-want +got):\n%s", diff)
	}
}

// With a TUN inbound, all port-53 traffic is answered by Xray's DNS so
// lookups follow the routing rules instead of leaking to the ISP.
func TestConfigTUNHijacksDNS(t *testing.T) {
	got := renderConfig(t, xrayconf.Plan{
		Node:    plainVLESS,
		Default: config.Direct,
		TUN:     &xrayconf.TUN{Name: "utun9", MTU: 1500},
	})
	var cfg struct {
		Inbounds []any `json:"inbounds"`
		Routing  struct {
			Rules []any `json:"rules"`
		} `json:"routing"`
	}
	mustUnmarshal(t, got, &cfg)
	wantInbound := `{
		"tag": "tun-in",
		"protocol": "tun",
		"settings": {"name": "utun9", "MTU": 1500},
		"sniffing": {"enabled": true, "destOverride": ["http", "tls", "quic"], "routeOnly": true}
	}`
	if diff := cmp.Diff([]any{decode(t, []byte(wantInbound))}, cfg.Inbounds); diff != "" {
		t.Errorf("inbounds mismatch (-want +got):\n%s", diff)
	}
	wantRules := `[
		{"ruleTag": "dns-hijack", "inboundTag": ["tun-in"], "port": "53", "outboundTag": "dns-out"},
		{"ruleTag": "default", "network": "tcp,udp", "outboundTag": "direct"}
	]`
	if diff := cmp.Diff(decode(t, []byte(wantRules)), any(cfg.Routing.Rules)); diff != "" {
		t.Errorf("rules mismatch (-want +got):\n%s", diff)
	}
}

func renderConfig(t *testing.T, p xrayconf.Plan) []byte {
	t.Helper()
	got, err := xrayconf.Config(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.LoadConfig("json", bytes.NewReader(got)); err != nil {
		t.Errorf("xray-core rejects the config: %v", err)
	}
	return got
}
