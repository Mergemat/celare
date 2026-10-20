// Package xrayconf renders celare's model into Xray-core JSON config.
package xrayconf

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/b-omarov/celare/internal/node"
)

type OutboundObject struct {
	Tag            string          `json:"tag"`
	Protocol       string          `json:"protocol"`
	Settings       any             `json:"settings,omitempty"`
	StreamSettings *StreamSettings `json:"streamSettings,omitempty"`
}

type StreamSettings struct {
	Network          string            `json:"network,omitempty"`
	Security         string            `json:"security,omitempty"`
	TLSSettings      *TLSSettings      `json:"tlsSettings,omitempty"`
	RealitySettings  *RealitySettings  `json:"realitySettings,omitempty"`
	WSSettings       *PathHost         `json:"wsSettings,omitempty"`
	XHTTPSettings    *XHTTPSettings    `json:"xhttpSettings,omitempty"`
	GRPCSettings     *GRPCSettings     `json:"grpcSettings,omitempty"`
	HTTPUpgrade      *PathHost         `json:"httpupgradeSettings,omitempty"`
	KCPSettings      *KCPSettings      `json:"kcpSettings,omitempty"`
	RawSettings      *RawSettings      `json:"rawSettings,omitempty"`
	HysteriaSettings *HysteriaSettings `json:"hysteriaSettings,omitempty"`
	Finalmask        json.RawMessage   `json:"finalmask,omitempty"`
}

type HysteriaSettings struct {
	Version int    `json:"version"`
	Auth    string `json:"auth"`
}

type GRPCSettings struct {
	ServiceName string `json:"serviceName,omitempty"`
	Authority   string `json:"authority,omitempty"`
	MultiMode   bool   `json:"multiMode,omitempty"`
}

type KCPSettings struct {
	MTU int `json:"mtu,omitempty"`
	TTI int `json:"tti,omitempty"`
}

type RawSettings struct {
	Header HTTPHeader `json:"header"`
}

type HTTPHeader struct {
	Type    string      `json:"type"`
	Request HTTPRequest `json:"request"`
}

type HTTPRequest struct {
	Path    []string            `json:"path"`
	Headers map[string][]string `json:"headers,omitempty"`
}

type XHTTPSettings struct {
	Path          string          `json:"path,omitempty"`
	Host          string          `json:"host,omitempty"`
	Mode          string          `json:"mode,omitempty"`
	XPaddingBytes string          `json:"xPaddingBytes,omitempty"`
	Extra         json.RawMessage `json:"extra,omitempty"`
}

// PathHost is the settings shape shared by WebSocket and HTTPUpgrade.
type PathHost struct {
	Path string `json:"path,omitempty"`
	Host string `json:"host,omitempty"`
}

type RealitySettings struct {
	ServerName    string `json:"serverName,omitempty"`
	Fingerprint   string `json:"fingerprint,omitempty"`
	PublicKey     string `json:"publicKey"`
	ShortID       string `json:"shortId,omitempty"`
	SpiderX       string `json:"spiderX,omitempty"`
	MLDSA65Verify string `json:"mldsa65Verify,omitempty"`
}

type TLSSettings struct {
	ServerName           string   `json:"serverName,omitempty"`
	Fingerprint          string   `json:"fingerprint,omitempty"`
	ALPN                 []string `json:"alpn,omitempty"`
	AllowInsecure        bool     `json:"allowInsecure,omitempty"`
	ECHConfigList        string   `json:"echConfigList,omitempty"`
	PinnedPeerCertSHA256 string   `json:"pinnedPeerCertSha256,omitempty"`
	VerifyPeerCertByName string   `json:"verifyPeerCertByName,omitempty"`
}

type vnext struct {
	Address string `json:"address"`
	Port    int    `json:"port"`
	Users   []user `json:"users"`
}

type user struct {
	ID         string `json:"id"`
	Encryption string `json:"encryption,omitempty"`
	Flow       string `json:"flow,omitempty"`
	Security   string `json:"security,omitempty"`
}

type server struct {
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Method   string `json:"method,omitempty"`
	Password string `json:"password,omitempty"`
}

// Outbound renders one node as an Xray outbound with the given tag.
func Outbound(n node.Node, tag string) (OutboundObject, error) {
	out := OutboundObject{Tag: tag, Protocol: n.Protocol}
	switch n.Protocol {
	case "trojan":
		out.Settings = map[string]any{"servers": []server{{Address: n.Address, Port: n.Port, Password: n.Password}}}
	case "vless":
		encryption := n.Encryption
		if encryption == "" {
			encryption = "none"
		}
		out.Settings = map[string]any{"vnext": []vnext{{
			Address: n.Address, Port: n.Port,
			Users: []user{{ID: n.UUID, Encryption: encryption, Flow: n.Flow}},
		}}}
	case "shadowsocks":
		out.Settings = map[string]any{"servers": []server{{Address: n.Address, Port: n.Port, Method: n.Cipher, Password: n.Password}}}
	case "hysteria2":
		out.Protocol = "hysteria"
		out.Settings = map[string]any{"version": 2, "address": n.Address, "port": n.Port}
	case "wireguard":
		out.Settings = map[string]any{
			"secretKey": n.WireGuard.PrivateKey,
			"address":   n.WireGuard.LocalAddresses,
			"peers": []map[string]any{{
				"publicKey": n.WireGuard.PeerPublicKey,
				"endpoint":  net.JoinHostPort(n.Address, strconv.Itoa(n.Port)),
			}},
			"mtu": n.WireGuard.MTU,
		}
		// WireGuard carries its own transport and encryption.
		return out, nil
	case "vmess":
		out.Settings = map[string]any{"vnext": []vnext{{
			Address: n.Address, Port: n.Port,
			Users: []user{{ID: n.UUID, Security: n.Cipher}},
		}}}
	default:
		return OutboundObject{}, fmt.Errorf("protocol %q has no Xray outbound", n.Protocol)
	}
	stream, err := streamSettings(n)
	if err != nil {
		return OutboundObject{}, err
	}
	out.StreamSettings = stream
	return out, nil
}

func streamSettings(n node.Node) (*StreamSettings, error) {
	t := n.Transport
	s := &StreamSettings{Network: network(t.Type), Security: n.Security.Type}
	if n.Protocol == "hysteria2" {
		s.Network = "hysteria"
		s.HysteriaSettings = &HysteriaSettings{Version: 2, Auth: n.Password}
	}
	switch s.Network {
	case "ws":
		s.WSSettings = &PathHost{Path: t.Path, Host: t.Host}
	case "xhttp":
		s.XHTTPSettings = &XHTTPSettings{Path: t.Path, Host: t.Host, Mode: t.Mode, XPaddingBytes: t.XPaddingBytes}
		if t.Extra != "" {
			if !json.Valid([]byte(t.Extra)) {
				return nil, fmt.Errorf("xhttp extra is not valid JSON")
			}
			s.XHTTPSettings.Extra = json.RawMessage(t.Extra)
		}
	case "grpc":
		s.GRPCSettings = &GRPCSettings{ServiceName: t.ServiceName, Authority: t.Authority, MultiMode: t.Mode == "multi"}
	case "httpupgrade":
		s.HTTPUpgrade = &PathHost{Path: t.Path, Host: t.Host}
	case "kcp":
		s.KCPSettings = &KCPSettings{MTU: t.MTU, TTI: t.TTI}
	case "raw":
		if t.HeaderType == "http" {
			s.RawSettings = &RawSettings{Header: httpHeader(t)}
		}
	}
	fm, err := finalmask(n)
	if err != nil {
		return nil, err
	}
	s.Finalmask = fm
	if n.Security.Type == "tls" {
		pins, err := hexPins(n.Security.PinnedPeerCertSHA256)
		if err != nil {
			return nil, err
		}
		s.TLSSettings = &TLSSettings{
			ServerName:           n.Security.SNI,
			Fingerprint:          n.Security.Fingerprint,
			ALPN:                 n.Security.ALPN,
			AllowInsecure:        n.Security.AllowInsecure,
			ECHConfigList:        n.Security.ECHConfig,
			PinnedPeerCertSHA256: pins,
			VerifyPeerCertByName: n.Security.VerifyPeerCertByName,
		}
	}
	if n.Security.Type == "reality" {
		s.RealitySettings = &RealitySettings{
			ServerName:    n.Security.SNI,
			Fingerprint:   n.Security.Fingerprint,
			PublicKey:     n.Security.PublicKey,
			ShortID:       n.Security.ShortID,
			SpiderX:       n.Security.SpiderX,
			MLDSA65Verify: n.Security.MLDSA65Verify,
		}
	}
	return s, nil
}

// hexPins converts certificate SHA-256 pins to the comma-separated hex
// Xray expects. Pins may be hex (with or without colons) or base64.
func hexPins(pins []string) (string, error) {
	out := make([]string, 0, len(pins))
	for _, pin := range pins {
		bare := strings.ReplaceAll(pin, ":", "")
		if b, err := hex.DecodeString(bare); err == nil && len(b) == 32 {
			out = append(out, strings.ToLower(bare))
			continue
		}
		if b, err := base64.StdEncoding.DecodeString(pin); err == nil && len(b) == 32 {
			out = append(out, hex.EncodeToString(b))
			continue
		}
		return "", fmt.Errorf("certificate pin %q is not a SHA-256 digest in hex or base64", pin)
	}
	return strings.Join(out, ","), nil
}

// network maps share-link transport names to Xray's current ones.
func network(t string) string {
	switch t {
	case "", "tcp":
		return "raw"
	default:
		return t
	}
}

// httpHeader builds a raw-transport HTTP header. Share links carry paths
// comma-separated; without one Xray clients conventionally request "/".
func httpHeader(t node.Transport) HTTPHeader {
	paths := strings.Split(t.Path, ",")
	if t.Path == "" {
		paths = []string{"/"}
	}
	h := HTTPHeader{Type: "http", Request: HTTPRequest{Path: paths}}
	if t.Host != "" {
		h.Request.Headers = map[string][]string{"Host": strings.Split(t.Host, ",")}
	}
	return h
}

// finalmask returns the link's finalmask with Hysteria2 salamander
// obfuscation and port hopping merged in, as Xray expects them there.
func finalmask(n node.Node) (json.RawMessage, error) {
	fm := map[string]any{}
	if n.Finalmask != "" {
		if err := json.Unmarshal([]byte(n.Finalmask), &fm); err != nil {
			return nil, fmt.Errorf("finalmask is not a JSON object: %w", err)
		}
	}
	if n.Obfs.Type != "" {
		udp, _ := fm["udp"].([]any)
		fm["udp"] = append(udp, map[string]any{
			"type":     n.Obfs.Type,
			"settings": map[string]any{"password": n.Obfs.Password},
		})
	}
	if n.PortHopping != "" {
		quic, _ := fm["quicParams"].(map[string]any)
		if quic == nil {
			quic = map[string]any{}
		}
		quic["udpHop"] = map[string]any{"ports": n.PortHopping}
		fm["quicParams"] = quic
	}
	if len(fm) == 0 {
		return nil, nil
	}
	return json.Marshal(fm)
}
