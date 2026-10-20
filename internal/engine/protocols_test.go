package engine_test

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Mergemat/celare/internal/config"
	"github.com/Mergemat/celare/internal/node"
	"github.com/Mergemat/celare/internal/xrayconf"
)

const testUUID = "22222222-3333-4444-9555-666666666666"

// Each case runs a real Xray server for one protocol and sends a request
// through celare's rendered client config. Reaching the "proxied" target
// proves the parsed-and-rendered client speaks the protocol.
func TestEveryProtocolCarriesTraffic(t *testing.T) {
	proxied := answer(t, "proxied")
	cert := selfSignedCert(t, "example.test")
	certs := fmt.Sprintf(`"certificates": [{"certificate": %s, "key": %s}]`, jsonLines(cert.certPEM), jsonLines(cert.keyPEM))
	tlsServer := `"security": "tls", "tlsSettings": {` + certs + `}`
	// Hysteria2 is QUIC, which refuses a handshake without ALPN h3.
	h3Server := `"security": "tls", "tlsSettings": {"alpn": ["h3"], ` + certs + `}`
	tlsClient := node.Security{Type: "tls", SNI: "example.test", PinnedPeerCertSHA256: []string{cert.pin}}
	ssKey := base64.StdEncoding.EncodeToString(make([]byte, 32))
	realityPriv, realityPub := x25519KeyPair(t)
	realityTarget := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(realityTarget.Close)

	cases := []struct {
		name    string
		inbound string // %d is the server port
		client  node.Node
	}{
		{
			name: "vless reality vision",
			inbound: `{"protocol": "vless", "settings": {"clients": [{"id": "` + testUUID + `", "flow": "xtls-rprx-vision"}], "decryption": "none"},
				"streamSettings": {"network": "raw", "security": "reality", "realitySettings": {
					"target": "` + strings.TrimPrefix(realityTarget.URL, "https://") + `", "serverNames": ["example.test"],
					"privateKey": "` + realityPriv + `", "shortIds": ["a3f1"]}}}`,
			client: node.Node{Protocol: "vless", UUID: testUUID, Flow: "xtls-rprx-vision", Encryption: "none",
				Transport: node.Transport{Type: "tcp"},
				Security:  node.Security{Type: "reality", SNI: "example.test", Fingerprint: "chrome", PublicKey: realityPub, ShortID: "a3f1"}},
		},
		{
			name: "vless xhttp tls",
			inbound: `{"protocol": "vless", "settings": {"clients": [{"id": "` + testUUID + `"}], "decryption": "none"},
				"streamSettings": {"network": "xhttp", "xhttpSettings": {"path": "/xh"}, ` + tlsServer + `}}`,
			client: node.Node{Protocol: "vless", UUID: testUUID, Encryption: "none",
				Transport: node.Transport{Type: "xhttp", Path: "/xh", Mode: "auto"}, Security: tlsClient},
		},
		{
			name: "vless grpc tls",
			inbound: `{"protocol": "vless", "settings": {"clients": [{"id": "` + testUUID + `"}], "decryption": "none"},
				"streamSettings": {"network": "grpc", "grpcSettings": {"serviceName": "svc"}, ` + tlsServer + `}}`,
			client: node.Node{Protocol: "vless", UUID: testUUID, Encryption: "none",
				Transport: node.Transport{Type: "grpc", ServiceName: "svc"}, Security: tlsClient},
		},
		{
			name: "vmess websocket",
			inbound: `{"protocol": "vmess", "settings": {"clients": [{"id": "` + testUUID + `"}]},
				"streamSettings": {"network": "ws", "wsSettings": {"path": "/ws"}}}`,
			client: node.Node{Protocol: "vmess", UUID: testUUID, Cipher: "auto",
				Transport: node.Transport{Type: "ws", Path: "/ws"}},
		},
		{
			name:    "trojan tls",
			inbound: `{"protocol": "trojan", "settings": {"clients": [{"password": "trojan-pw"}]}, "streamSettings": {"network": "raw", ` + tlsServer + `}}`,
			client:  node.Node{Protocol: "trojan", Password: "trojan-pw", Transport: node.Transport{Type: "tcp"}, Security: tlsClient},
		},
		{
			name: "trojan httpupgrade tls",
			inbound: `{"protocol": "trojan", "settings": {"clients": [{"password": "trojan-pw"}]},
				"streamSettings": {"network": "httpupgrade", "httpupgradeSettings": {"path": "/up"}, ` + tlsServer + `}}`,
			client: node.Node{Protocol: "trojan", Password: "trojan-pw",
				Transport: node.Transport{Type: "httpupgrade", Path: "/up"}, Security: tlsClient},
		},
		{
			name:    "shadowsocks 2022",
			inbound: `{"protocol": "shadowsocks", "settings": {"method": "2022-blake3-aes-256-gcm", "password": "` + ssKey + `", "network": "tcp,udp"}}`,
			client:  node.Node{Protocol: "shadowsocks", Cipher: "2022-blake3-aes-256-gcm", Password: ssKey, Transport: node.Transport{Type: "tcp"}},
		},
		{
			name:    "shadowsocks aes-256-gcm",
			inbound: `{"protocol": "shadowsocks", "settings": {"method": "aes-256-gcm", "password": "legacy-pw", "network": "tcp,udp"}}`,
			client:  node.Node{Protocol: "shadowsocks", Cipher: "aes-256-gcm", Password: "legacy-pw", Transport: node.Transport{Type: "tcp"}},
		},
		{
			name: "hysteria2 salamander",
			inbound: `{"protocol": "hysteria", "settings": {"version": 2, "clients": [{"auth": "hy-auth"}]},
				"streamSettings": {"network": "hysteria", "hysteriaSettings": {"version": 2},
					"finalmask": {"udp": [{"type": "salamander", "settings": {"password": "obfs-pw"}}]}, ` + h3Server + `}}`,
			client: node.Node{Protocol: "hysteria2", Password: "hy-auth",
				Obfs: node.Obfs{Type: "salamander", Password: "obfs-pw"}, Security: tlsClient},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			port := freePort(t)
			var inbound map[string]any
			if err := json.Unmarshal([]byte(tc.inbound), &inbound); err != nil {
				t.Fatalf("bad inbound fixture: %v", err)
			}
			inbound["listen"], inbound["port"] = "127.0.0.1", port
			server, _ := json.Marshal(map[string]any{
				"inbounds":  []any{inbound},
				"outbounds": []any{map[string]any{"protocol": "freedom", "settings": map[string]any{"redirect": proxied}}},
			})
			startEngine(t, string(server))

			tc.client.Address, tc.client.Port = "127.0.0.1", port
			socks := freePort(t)
			cfg, err := xrayconf.Config(xrayconf.Plan{Node: tc.client, Default: config.Proxy, SOCKSPort: socks})
			if err != nil {
				t.Fatal(err)
			}
			startEngine(t, string(cfg))

			if got := get(t, socksClient(t, socks), "http://198.51.100.1/"); got != "proxied" {
				t.Errorf("request through %s answered %q, want proxied", tc.name, got)
			}
		})
	}
}

type testCert struct {
	certPEM, keyPEM string
	pin             string // hex SHA-256 of the DER certificate
}

func selfSignedCert(t *testing.T, host string) testCert {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(der)
	return testCert{
		certPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		keyPEM:  string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})),
		pin:     hex.EncodeToString(sum[:]),
	}
}

// jsonLines encodes PEM the way Xray's inline certificate field wants it:
// a JSON array of lines.
func jsonLines(pemText string) string {
	b, _ := json.Marshal(strings.Split(strings.TrimSpace(pemText), "\n"))
	return string(b)
}

func x25519KeyPair(t *testing.T) (private, public string) {
	t.Helper()
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding
	return enc.EncodeToString(k.Bytes()), enc.EncodeToString(k.PublicKey().Bytes())
}
