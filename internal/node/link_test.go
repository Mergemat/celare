package node_test

import (
	"testing"

	"github.com/b-omarov/celare/internal/node"
	"github.com/google/go-cmp/cmp"
)

// Trojan fixtures are real 3x-ui share links with the credentials and
// addresses replaced; only the query parameters are kept verbatim. The
// others are 3x-ui's own golden snapshots (frontend/src/test/__snapshots__/
// inbound-link.test.ts.snap), which carry fake credentials already.

func TestParseLinkTrojanTLS(t *testing.T) {
	got, err := node.ParseLink("trojan://5f2b9c1e7a3d4e6f8091a2b3c4d5e6f7@203.0.113.10:8444?fp=chrome&sni=203.0.113.10&allowInsecure=0&type=tcp&insecure=0&headerType=none&security=tls#Tokyo-Trojan-Test")
	if err != nil {
		t.Fatal(err)
	}
	want := node.Node{
		Name:     "Tokyo-Trojan-Test",
		Protocol: "trojan",
		Address:  "203.0.113.10",
		Port:     8444,
		Password: "5f2b9c1e7a3d4e6f8091a2b3c4d5e6f7",
		Transport: node.Transport{
			Type: "tcp",
		},
		Security: node.Security{
			Type:        "tls",
			SNI:         "203.0.113.10",
			Fingerprint: "chrome",
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ParseLink mismatch (-want +got):\n%s", diff)
	}
}

func TestParseLinkTrojanWithALPNAndECH(t *testing.T) {
	got, err := node.ParseLink("trojan://9d8c7b6a5f4e3d2c1b0a9f8e7d6c5b4a@203.0.113.20:59441?alpn=h2%2Chttp%2F1.1&ech=AEX%2BDQBBAAAgACBzeW50aGV0aWM&fp=chrome&security=tls&sni=203.0.113.20&type=tcp#amen-test")
	if err != nil {
		t.Fatal(err)
	}
	want := node.Node{
		Name:     "amen-test",
		Protocol: "trojan",
		Address:  "203.0.113.20",
		Port:     59441,
		Password: "9d8c7b6a5f4e3d2c1b0a9f8e7d6c5b4a",
		Transport: node.Transport{
			Type: "tcp",
		},
		Security: node.Security{
			Type:        "tls",
			SNI:         "203.0.113.20",
			Fingerprint: "chrome",
			ALPN:        []string{"h2", "http/1.1"},
			ECHConfig:   "AEX+DQBBAAAgACBzeW50aGV0aWM",
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ParseLink mismatch (-want +got):\n%s", diff)
	}
}

func TestParseLinkVLESSRealityVision(t *testing.T) {
	got, err := node.ParseLink("vless://22222222-3333-4444-9555-666666666666@example.test:443?type=tcp&encryption=none&security=reality&support-x25519mlkem768=true&pbk=Tx5yj1bRcOPHkdvT2pIAQ2zh0gQ8m4OPdnzqXJxxV3o&fp=chrome&sni=yahoo.com&sid=a3f1&spx=%2Fd08ed99bd9afc60&flow=xtls-rprx-vision#parity-test")
	if err != nil {
		t.Fatal(err)
	}
	want := node.Node{
		Name:       "parity-test",
		Protocol:   "vless",
		Address:    "example.test",
		Port:       443,
		UUID:       "22222222-3333-4444-9555-666666666666",
		Flow:       "xtls-rprx-vision",
		Encryption: "none",
		Transport: node.Transport{
			Type: "tcp",
		},
		Security: node.Security{
			Type:        "reality",
			SNI:         "yahoo.com",
			Fingerprint: "chrome",
			PublicKey:   "Tx5yj1bRcOPHkdvT2pIAQ2zh0gQ8m4OPdnzqXJxxV3o",
			ShortID:     "a3f1",
			SpiderX:     "/d08ed99bd9afc60",
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ParseLink mismatch (-want +got):\n%s", diff)
	}
}

func TestParseLinkTrojanWebSocket(t *testing.T) {
	got, err := node.ParseLink("trojan://trojan-test-pw-XYZ@example.test:443?type=ws&path=%2Ftrojan&host=trojan.example.test&security=tls&fp=chrome&alpn=h2%2Chttp%2F1.1&sni=trojan.example.test#parity-test")
	if err != nil {
		t.Fatal(err)
	}
	want := node.Node{
		Name:     "parity-test",
		Protocol: "trojan",
		Address:  "example.test",
		Port:     443,
		Password: "trojan-test-pw-XYZ",
		Transport: node.Transport{
			Type: "ws",
			Path: "/trojan",
			Host: "trojan.example.test",
		},
		Security: node.Security{
			Type:        "tls",
			SNI:         "trojan.example.test",
			Fingerprint: "chrome",
			ALPN:        []string{"h2", "http/1.1"},
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ParseLink mismatch (-want +got):\n%s", diff)
	}
}

func TestParseLinkVMessTLS(t *testing.T) {
	got, err := node.ParseLink("vmess://ewogICJ2IjogIjIiLAogICJwcyI6ICJwYXJpdHktdGVzdCIsCiAgImFkZCI6ICJleGFtcGxlLnRlc3QiLAogICJwb3J0IjogODQ0MywKICAiaWQiOiAiMTExMTExMTEtMjIyMi00MzMzLTg0NDQtNTU1NTU1NTU1NTU1IiwKICAic2N5IjogImF1dG8iLAogICJuZXQiOiAidGNwIiwKICAidGxzIjogInRscyIsCiAgInR5cGUiOiAibm9uZSIsCiAgInNuaSI6ICJ2bWVzcy5leGFtcGxlLnRlc3QiLAogICJmcCI6ICJjaHJvbWUiLAogICJhbHBuIjogImgyLGh0dHAvMS4xIgp9")
	if err != nil {
		t.Fatal(err)
	}
	want := node.Node{
		Name:     "parity-test",
		Protocol: "vmess",
		Address:  "example.test",
		Port:     8443,
		UUID:     "11111111-2222-4333-8444-555555555555",
		Cipher:   "auto",
		Transport: node.Transport{
			Type: "tcp",
		},
		Security: node.Security{
			Type:        "tls",
			SNI:         "vmess.example.test",
			Fingerprint: "chrome",
			ALPN:        []string{"h2", "http/1.1"},
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ParseLink mismatch (-want +got):\n%s", diff)
	}
}

// SIP022: 2022 ciphers carry method, server key and user key in plain
// percent-encoded userinfo. Xray takes the two keys as "server:user".
func TestParseLinkShadowsocks2022(t *testing.T) {
	got, err := node.ParseLink("ss://2022-blake3-aes-256-gcm:ZmFrZS1zZXJ2ZXItcGFzc3dvcmQtMDAwMQ%3D%3D:dGVzdC1jbGllbnQtcGFzc3dvcmQtMQ%3D%3D@example.test:8388?type=tcp#parity-test")
	if err != nil {
		t.Fatal(err)
	}
	want := node.Node{
		Name:     "parity-test",
		Protocol: "shadowsocks",
		Address:  "example.test",
		Port:     8388,
		Cipher:   "2022-blake3-aes-256-gcm",
		Password: "ZmFrZS1zZXJ2ZXItcGFzc3dvcmQtMDAwMQ==:dGVzdC1jbGllbnQtcGFzc3dvcmQtMQ==",
		Transport: node.Transport{
			Type: "tcp",
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ParseLink mismatch (-want +got):\n%s", diff)
	}
}

// SIP002: legacy ciphers carry base64url("method:password") as userinfo.
func TestParseLinkShadowsocksLegacyBase64(t *testing.T) {
	got, err := node.ParseLink("ss://YWVzLTI1Ni1nY206bGVnYWN5LXRlc3QtcHc@example.test:8388?type=tcp#legacy")
	if err != nil {
		t.Fatal(err)
	}
	want := node.Node{
		Name:     "legacy",
		Protocol: "shadowsocks",
		Address:  "example.test",
		Port:     8388,
		Cipher:   "aes-256-gcm",
		Password: "legacy-test-pw",
		Transport: node.Transport{
			Type: "tcp",
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ParseLink mismatch (-want +got):\n%s", diff)
	}
}

func TestParseLinkHysteria2TLS(t *testing.T) {
	got, err := node.ParseLink("hysteria2://hyst-v1-auth-XYZ@example.test:36715?security=tls&fp=chrome&alpn=h3&sni=hysteria.example.test#parity-test")
	if err != nil {
		t.Fatal(err)
	}
	want := node.Node{
		Name:     "parity-test",
		Protocol: "hysteria2",
		Address:  "example.test",
		Port:     36715,
		Password: "hyst-v1-auth-XYZ",
		Security: node.Security{
			Type:        "tls",
			SNI:         "hysteria.example.test",
			Fingerprint: "chrome",
			ALPN:        []string{"h3"},
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ParseLink mismatch (-want +got):\n%s", diff)
	}
}

// The Hysteria2 URI spec allows hy2:// as a short scheme.
func TestParseLinkHy2SchemeIsHysteria2(t *testing.T) {
	got, err := node.ParseLink("hy2://auth@example.test:443?security=tls&sni=example.test#short")
	if err != nil {
		t.Fatal(err)
	}
	if got.Protocol != "hysteria2" {
		t.Errorf("Protocol = %q, want hysteria2", got.Protocol)
	}
}

// Parameter names are the ones 3x-ui's genHysteriaLink emits.
func TestParseLinkHysteria2ObfsAndPortHopping(t *testing.T) {
	got, err := node.ParseLink("hysteria2://auth@example.test:443?security=tls&sni=example.test&obfs=salamander&obfs-password=obfs-test-pw&mport=20000-30000#hop")
	if err != nil {
		t.Fatal(err)
	}
	want := node.Node{
		Name:        "hop",
		Protocol:    "hysteria2",
		Address:     "example.test",
		Port:        443,
		Password:    "auth",
		PortHopping: "20000-30000",
		Obfs: node.Obfs{
			Type:     "salamander",
			Password: "obfs-test-pw",
		},
		Security: node.Security{
			Type: "tls",
			SNI:  "example.test",
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ParseLink mismatch (-want +got):\n%s", diff)
	}
}

// The userinfo is the client's private key; publickey is the server's.
func TestParseLinkWireGuard(t *testing.T) {
	got, err := node.ParseLink("wireguard://QGVlb2dXc1ZTWGw0ZXBzZndsWmtMaUM5MUlNYjBHWFdYbz0%3D@wg.example.test:51820?publickey=Piehk2n8UewhMHMyJiBS%2BSxn%2FOK0FalyFW1GAGzokHM%3D&address=10.0.0.2%2F32&mtu=1420#wg-peer-1")
	if err != nil {
		t.Fatal(err)
	}
	want := node.Node{
		Name:     "wg-peer-1",
		Protocol: "wireguard",
		Address:  "wg.example.test",
		Port:     51820,
		WireGuard: node.WireGuard{
			PrivateKey:     "QGVlb2dXc1ZTWGw0ZXBzZndsWmtMaUM5MUlNYjBHWFdYbz0=",
			PeerPublicKey:  "Piehk2n8UewhMHMyJiBS+Sxn/OK0FalyFW1GAGzokHM=",
			LocalAddresses: []string{"10.0.0.2/32"},
			MTU:            1420,
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ParseLink mismatch (-want +got):\n%s", diff)
	}
}
