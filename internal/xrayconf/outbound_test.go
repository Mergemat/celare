package xrayconf_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/Mergemat/celare/internal/node"
	"github.com/Mergemat/celare/internal/xrayconf"
	"github.com/google/go-cmp/cmp"
	"github.com/xtls/xray-core/core"
	_ "github.com/xtls/xray-core/main/distro/all"
)

// Expected JSON is written by hand from Xray's documented config format;
// every case is also loaded by xray-core itself.

func TestOutboundTrojanTLS(t *testing.T) {
	assertOutbound(t, node.Node{
		Protocol:  "trojan",
		Address:   "203.0.113.10",
		Port:      8444,
		Password:  "pw",
		Transport: node.Transport{Type: "tcp", HeaderType: "none"},
		Security: node.Security{
			Type:        "tls",
			SNI:         "203.0.113.10",
			Fingerprint: "chrome",
		},
	}, `{
		"tag": "proxy",
		"protocol": "trojan",
		"settings": {"servers": [{"address": "203.0.113.10", "port": 8444, "password": "pw"}]},
		"streamSettings": {
			"network": "raw",
			"security": "tls",
			"tlsSettings": {"serverName": "203.0.113.10", "fingerprint": "chrome"}
		}
	}`)
}

func TestOutboundVLESSRealityVision(t *testing.T) {
	assertOutbound(t, node.Node{
		Protocol:   "vless",
		Address:    "example.test",
		Port:       443,
		UUID:       "22222222-3333-4444-9555-666666666666",
		Flow:       "xtls-rprx-vision",
		Encryption: "none",
		Transport:  node.Transport{Type: "tcp"},
		Security: node.Security{
			Type:        "reality",
			SNI:         "yahoo.com",
			Fingerprint: "chrome",
			PublicKey:   "Tx5yj1bRcOPHkdvT2pIAQ2zh0gQ8m4OPdnzqXJxxV3o",
			ShortID:     "a3f1",
			SpiderX:     "/d08ed99bd9afc60",
		},
	}, `{
		"tag": "proxy",
		"protocol": "vless",
		"settings": {"vnext": [{
			"address": "example.test",
			"port": 443,
			"users": [{"id": "22222222-3333-4444-9555-666666666666", "encryption": "none", "flow": "xtls-rprx-vision"}]
		}]},
		"streamSettings": {
			"network": "raw",
			"security": "reality",
			"realitySettings": {
				"serverName": "yahoo.com",
				"fingerprint": "chrome",
				"publicKey": "Tx5yj1bRcOPHkdvT2pIAQ2zh0gQ8m4OPdnzqXJxxV3o",
				"shortId": "a3f1",
				"spiderX": "/d08ed99bd9afc60"
			}
		}
	}`)
}

// Pins arrive as base64 (3x-ui VLESS/Trojan links) or hex (Hysteria2
// links); Xray only accepts hex. The hex below is shasum's output for the
// same digest.
func TestOutboundVMessWebSocketTLSPins(t *testing.T) {
	assertOutbound(t, node.Node{
		Protocol:  "vmess",
		Address:   "example.test",
		Port:      443,
		UUID:      "11111111-2222-4333-8444-555555555555",
		Cipher:    "auto",
		Transport: node.Transport{Type: "ws", Path: "/ws", Host: "cdn.example.test"},
		Security: node.Security{
			Type:                 "tls",
			SNI:                  "cdn.example.test",
			Fingerprint:          "chrome",
			ALPN:                 []string{"h2", "http/1.1"},
			PinnedPeerCertSHA256: []string{"YXDBjYqR/X9fqbty/gK1y6zsF/D11qyva36rJAsTfVo=", "6170c18d8a91fd7f5fa9bb72fe02b5cbacec17f0f5d6acaf6b7eab240b137d5a"},
			VerifyPeerCertByName: "cert.example.test",
		},
	}, `{
		"tag": "proxy",
		"protocol": "vmess",
		"settings": {"vnext": [{
			"address": "example.test",
			"port": 443,
			"users": [{"id": "11111111-2222-4333-8444-555555555555", "security": "auto"}]
		}]},
		"streamSettings": {
			"network": "ws",
			"security": "tls",
			"tlsSettings": {
				"serverName": "cdn.example.test",
				"fingerprint": "chrome",
				"alpn": ["h2", "http/1.1"],
				"pinnedPeerCertSha256": "6170c18d8a91fd7f5fa9bb72fe02b5cbacec17f0f5d6acaf6b7eab240b137d5a,6170c18d8a91fd7f5fa9bb72fe02b5cbacec17f0f5d6acaf6b7eab240b137d5a",
				"verifyPeerCertByName": "cert.example.test"
			},
			"wsSettings": {"path": "/ws", "host": "cdn.example.test"}
		}
	}`)
}

func TestOutboundShadowsocks2022(t *testing.T) {
	assertOutbound(t, node.Node{
		Protocol:  "shadowsocks",
		Address:   "example.test",
		Port:      8388,
		Cipher:    "2022-blake3-aes-256-gcm",
		Password:  "YXDBjYqR/X9fqbty/gK1y6zsF/D11qyva36rJAsTfVo=:YXDBjYqR/X9fqbty/gK1y6zsF/D11qyva36rJAsTfVo=",
		Transport: node.Transport{Type: "tcp"},
	}, `{
		"tag": "proxy",
		"protocol": "shadowsocks",
		"settings": {"servers": [{
			"address": "example.test",
			"port": 8388,
			"method": "2022-blake3-aes-256-gcm",
			"password": "YXDBjYqR/X9fqbty/gK1y6zsF/D11qyva36rJAsTfVo=:YXDBjYqR/X9fqbty/gK1y6zsF/D11qyva36rJAsTfVo="
		}]},
		"streamSettings": {"network": "raw"}
	}`)
}

func TestOutboundVLESSXHTTP(t *testing.T) {
	assertOutbound(t, node.Node{
		Protocol:   "vless",
		Address:    "example.test",
		Port:       443,
		UUID:       "22222222-3333-4444-9555-666666666666",
		Encryption: "none",
		Transport: node.Transport{
			Type:          "xhttp",
			Path:          "/xh",
			Host:          "cdn.example.test",
			Mode:          "packet-up",
			XPaddingBytes: "100-1000",
			Extra:         `{"xmux":{"maxConcurrency":"16-32"}}`,
		},
		Security: node.Security{Type: "tls", SNI: "cdn.example.test"},
	}, `{
		"tag": "proxy",
		"protocol": "vless",
		"settings": {"vnext": [{
			"address": "example.test",
			"port": 443,
			"users": [{"id": "22222222-3333-4444-9555-666666666666", "encryption": "none"}]
		}]},
		"streamSettings": {
			"network": "xhttp",
			"security": "tls",
			"tlsSettings": {"serverName": "cdn.example.test"},
			"xhttpSettings": {
				"path": "/xh",
				"host": "cdn.example.test",
				"mode": "packet-up",
				"xPaddingBytes": "100-1000",
				"extra": {"xmux": {"maxConcurrency": "16-32"}}
			}
		}
	}`)
}

// Xray splits Hysteria2 across settings, hysteriaSettings and finalmask;
// salamander and port hopping live in finalmask and must merge with any
// finalmask the link already carried.
func TestOutboundHysteria2(t *testing.T) {
	assertOutbound(t, node.Node{
		Protocol:    "hysteria2",
		Address:     "example.test",
		Port:        443,
		Password:    "auth",
		PortHopping: "20000-30000",
		Obfs:        node.Obfs{Type: "salamander", Password: "obfs-test-pw"},
		Finalmask:   `{"quicParams":{"congestion":"bbr"}}`,
		Security:    node.Security{Type: "tls", SNI: "example.test", ALPN: []string{"h3"}},
	}, `{
		"tag": "proxy",
		"protocol": "hysteria",
		"settings": {"version": 2, "address": "example.test", "port": 443},
		"streamSettings": {
			"network": "hysteria",
			"security": "tls",
			"tlsSettings": {"serverName": "example.test", "alpn": ["h3"]},
			"hysteriaSettings": {"version": 2, "auth": "auth"},
			"finalmask": {
				"udp": [
					{"type": "salamander", "settings": {"password": "obfs-test-pw"}},
					{"type": "udphop", "settings": {"mode": "intervallocal,intervalremote", "remotePorts": "20000-30000"}}
				],
				"quicParams": {"congestion": "bbr"}
			}
		}
	}`)
}

// Links from servers on older Xray carry port hopping as
// quicParams.udpHop, which current Xray ignores; it must still hop.
func TestOutboundHysteria2LegacyUDPHop(t *testing.T) {
	assertOutbound(t, node.Node{
		Protocol:  "hysteria2",
		Address:   "example.test",
		Port:      443,
		Password:  "auth",
		Finalmask: `{"quicParams":{"udpHop":{"ports":"20000-30000","interval":"5-10"}}}`,
		Security:  node.Security{Type: "tls", SNI: "example.test", ALPN: []string{"h3"}},
	}, `{
		"tag": "proxy",
		"protocol": "hysteria",
		"settings": {"version": 2, "address": "example.test", "port": 443},
		"streamSettings": {
			"network": "hysteria",
			"security": "tls",
			"tlsSettings": {"serverName": "example.test", "alpn": ["h3"]},
			"hysteriaSettings": {"version": 2, "auth": "auth"},
			"finalmask": {
				"udp": [{"type": "udphop", "settings": {"mode": "intervallocal,intervalremote", "remotePorts": "20000-30000", "interval": "5-10"}}]
			}
		}
	}`)
}

func TestOutboundWireGuard(t *testing.T) {
	assertOutbound(t, node.Node{
		Protocol: "wireguard",
		Address:  "2001:db8::1",
		Port:     51820,
		WireGuard: node.WireGuard{
			PrivateKey:     "YXDBjYqR/X9fqbty/gK1y6zsF/D11qyva36rJAsTfVo=",
			PeerPublicKey:  "YXDBjYqR/X9fqbty/gK1y6zsF/D11qyva36rJAsTfVo=",
			LocalAddresses: []string{"10.0.0.2/32"},
			MTU:            1420,
		},
	}, `{
		"tag": "proxy",
		"protocol": "wireguard",
		"settings": {
			"secretKey": "YXDBjYqR/X9fqbty/gK1y6zsF/D11qyva36rJAsTfVo=",
			"address": ["10.0.0.2/32"],
			"peers": [{"publicKey": "YXDBjYqR/X9fqbty/gK1y6zsF/D11qyva36rJAsTfVo=", "endpoint": "[2001:db8::1]:51820"}],
			"mtu": 1420
		}
	}`)
}

func assertOutbound(t *testing.T, n node.Node, wantJSON string) {
	t.Helper()
	out, err := xrayconf.Outbound(n, "proxy")
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(decode(t, []byte(wantJSON)), decode(t, got)); diff != "" {
		t.Errorf("outbound mismatch (-want +got):\n%s", diff)
	}
	assertXrayAccepts(t, got)
}

func assertXrayAccepts(t *testing.T, outbound []byte) {
	t.Helper()
	cfg := `{"outbounds":[` + string(outbound) + `]}`
	if _, err := core.LoadConfig("json", bytes.NewBufferString(cfg)); err != nil {
		t.Errorf("xray-core rejects the outbound: %v", err)
	}
}

func decode(t *testing.T, b []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("invalid JSON %s: %v", b, err)
	}
	return v
}

func mustUnmarshal(t *testing.T, b []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("invalid JSON %s: %v", b, err)
	}
}
