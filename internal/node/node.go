// Package node holds the protocol-neutral description of a proxy server
// and converts share links into it.
package node

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type Node struct {
	Name       string
	Protocol   string
	Address    string
	Port       int
	Password   string
	UUID       string
	Flow       string
	Encryption string
	Cipher     string
	// PortHopping is a Hysteria2 port range such as "20000-30000".
	PortHopping string
	Obfs        Obfs
	WireGuard   WireGuard
	// Finalmask is Xray's raw JSON streamSettings.finalmask object.
	Finalmask string
	Transport Transport
	Security  Security
}

type WireGuard struct {
	PrivateKey     string
	PeerPublicKey  string
	LocalAddresses []string
	MTU            int
}

type Obfs struct {
	Type     string
	Password string
}

type Transport struct {
	Type          string
	Path          string
	Host          string
	Mode          string
	XPaddingBytes string
	ServiceName   string
	Authority     string
	HeaderType    string
	MTU           int
	TTI           int
	// Extra is XHTTP's raw JSON "extra" object, passed to Xray untouched.
	Extra string
}

type Security struct {
	Type        string
	SNI         string
	Fingerprint string
	ALPN        []string
	ECHConfig   string
	PublicKey   string
	ShortID     string
	SpiderX     string
	// MLDSA65Verify is REALITY's post-quantum signature verification key.
	MLDSA65Verify        string
	PinnedPeerCertSHA256 []string
	VerifyPeerCertByName string
	AllowInsecure        bool
}

func ParseLink(uri string) (Node, error) {
	if payload, ok := strings.CutPrefix(uri, "vmess://"); ok {
		return parseVMess(payload)
	}
	u, err := url.Parse(uri)
	if err != nil {
		// url.Error repeats the whole link, credentials included; keep
		// only the reason.
		return Node{}, fmt.Errorf("share link is not a valid URL: %w", errors.Unwrap(err))
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		return Node{}, fmt.Errorf("share link has invalid port %q", u.Port())
	}
	q := u.Query()
	mtu, err := optionalInt(q, "mtu")
	if err != nil {
		return Node{}, err
	}
	tti, err := optionalInt(q, "tti")
	if err != nil {
		return Node{}, err
	}
	n := Node{
		Name:      u.Fragment,
		Protocol:  u.Scheme,
		Address:   u.Hostname(),
		Port:      port,
		Finalmask: q.Get("fm"),
		Transport: Transport{
			Type:          q.Get("type"),
			Path:          q.Get("path"),
			Host:          q.Get("host"),
			Mode:          q.Get("mode"),
			XPaddingBytes: q.Get("x_padding_bytes"),
			Extra:         q.Get("extra"),
			ServiceName:   q.Get("serviceName"),
			Authority:     q.Get("authority"),
			HeaderType:    q.Get("headerType"),
			MTU:           mtu,
			TTI:           tti,
		},
		Security: Security{
			Type:                 q.Get("security"),
			SNI:                  q.Get("sni"),
			Fingerprint:          q.Get("fp"),
			ALPN:                 splitList(q.Get("alpn")),
			ECHConfig:            q.Get("ech"),
			PublicKey:            q.Get("pbk"),
			ShortID:              q.Get("sid"),
			SpiderX:              q.Get("spx"),
			MLDSA65Verify:        q.Get("pqv"),
			PinnedPeerCertSHA256: splitList(firstNonEmpty(q.Get("pcs"), q.Get("pinSHA256"))),
			VerifyPeerCertByName: q.Get("vcn"),
			AllowInsecure:        q.Get("allowInsecure") == "1" || q.Get("insecure") == "1",
		},
	}
	switch u.Scheme {
	case "vless":
		n.UUID = u.User.Username()
		n.Flow = q.Get("flow")
		n.Encryption = q.Get("encryption")
	case "trojan":
		n.Password = u.User.Username()
	case "hysteria2", "hy2":
		n.Protocol = "hysteria2"
		n.Password = u.User.Username()
		n.PortHopping = q.Get("mport")
		n.Obfs = Obfs{Type: q.Get("obfs"), Password: q.Get("obfs-password")}
	case "wireguard":
		n.WireGuard = WireGuard{
			PrivateKey:     u.User.Username(),
			PeerPublicKey:  q.Get("publickey"),
			LocalAddresses: splitList(q.Get("address")),
			MTU:            mtu,
		}
		n.Transport.MTU = 0
	case "ss":
		n.Protocol = "shadowsocks"
		n.Cipher, n.Password, err = shadowsocksCredentials(u.User)
		if err != nil {
			return Node{}, err
		}
		if plugin := q.Get("plugin"); plugin != "" {
			if n.Transport, err = shadowsocksPlugin(plugin); err != nil {
				return Node{}, err
			}
		}
	default:
		return Node{}, fmt.Errorf("unsupported share link scheme %q", u.Scheme)
	}
	return n, nil
}

func splitList(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

// vmessLink is the base64 JSON payload of a vmess:// link (the v2rayN
// format). Port is a json.Number because generators disagree on whether
// it is a number or a string.
type vmessLink struct {
	Name     string      `json:"ps"`
	Address  string      `json:"add"`
	Port     json.Number `json:"port"`
	ID       string      `json:"id"`
	Cipher   string      `json:"scy"`
	Network  string      `json:"net"`
	Security string      `json:"tls"`
	Path     string      `json:"path"`
	Host     string      `json:"host"`
	SNI      string      `json:"sni"`
	FP       string      `json:"fp"`
	ALPN     string      `json:"alpn"`
	// Type means the header type for tcp/kcp, "multi" for grpc and the
	// mode for xhttp.
	Type      string      `json:"type"`
	Authority string      `json:"authority"`
	MTU       json.Number `json:"mtu"`
	TTI       json.Number `json:"tti"`
	Pins      string      `json:"pcs"`
	VCN       string      `json:"vcn"`
	Finalmask string      `json:"fm"`
}

func parseVMess(payload string) (Node, error) {
	raw, err := decodeBase64(payload)
	if err != nil {
		return Node{}, fmt.Errorf("vmess link payload is not base64: %w", err)
	}
	var v vmessLink
	if err := json.Unmarshal(raw, &v); err != nil {
		return Node{}, fmt.Errorf("vmess link payload is not JSON: %w", err)
	}
	port, err := strconv.Atoi(v.Port.String())
	if err != nil {
		return Node{}, fmt.Errorf("vmess link has invalid port %q", v.Port)
	}
	n := Node{
		Name:      v.Name,
		Protocol:  "vmess",
		Address:   v.Address,
		Port:      port,
		UUID:      v.ID,
		Cipher:    v.Cipher,
		Finalmask: v.Finalmask,
		Transport: Transport{
			Type:      v.Network,
			Path:      v.Path,
			Host:      v.Host,
			Authority: v.Authority,
		},
		Security: Security{
			Type:                 v.Security,
			SNI:                  v.SNI,
			Fingerprint:          v.FP,
			ALPN:                 splitList(v.ALPN),
			PinnedPeerCertSHA256: splitList(v.Pins),
			VerifyPeerCertByName: v.VCN,
		},
	}
	switch v.Network {
	case "grpc":
		n.Transport.ServiceName, n.Transport.Path = v.Path, ""
		if v.Type == "multi" {
			n.Transport.Mode = "multi"
		}
	case "xhttp":
		n.Transport.Mode = v.Type
	default:
		n.Transport.HeaderType = v.Type
	}
	if v.MTU != "" {
		if n.Transport.MTU, err = strconv.Atoi(v.MTU.String()); err != nil {
			return Node{}, fmt.Errorf("vmess link has invalid mtu %q", v.MTU)
		}
	}
	if v.TTI != "" {
		if n.Transport.TTI, err = strconv.Atoi(v.TTI.String()); err != nil {
			return Node{}, fmt.Errorf("vmess link has invalid tti %q", v.TTI)
		}
	}
	return n, nil
}

// decodeBase64 accepts standard and URL-safe alphabets, with or without
// padding; share-link generators use all four.
func decodeBase64(s string) ([]byte, error) {
	s = strings.TrimRight(strings.TrimSpace(s), "=")
	if strings.ContainsAny(s, "-_") {
		return base64.RawURLEncoding.DecodeString(s)
	}
	return base64.RawStdEncoding.DecodeString(s)
}

// shadowsocksCredentials reads SIP022 userinfo (method:key[:key], plain)
// or SIP002 userinfo (base64 of method:password).
func shadowsocksCredentials(user *url.Userinfo) (cipher, password string, err error) {
	if pw, ok := user.Password(); ok {
		return user.Username(), pw, nil
	}
	raw, err := decodeBase64(user.Username())
	if err != nil {
		return "", "", fmt.Errorf("ss link userinfo is neither method:password nor base64: %w", err)
	}
	cipher, password, ok := strings.Cut(string(raw), ":")
	if !ok {
		return "", "", errors.New("ss link userinfo decodes without a method:password separator")
	}
	return cipher, password, nil
}

func optionalInt(q url.Values, key string) (int, error) {
	v := q.Get(key)
	if v == "" {
		return 0, nil
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("share link has invalid %s %q", key, v)
	}
	return i, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// shadowsocksPlugin maps the one SIP003 plugin Xray can express natively:
// obfs-local with obfs=http becomes a TCP transport with an HTTP header.
func shadowsocksPlugin(plugin string) (Transport, error) {
	name, opts, _ := strings.Cut(plugin, ";")
	if name != "obfs-local" && name != "simple-obfs" {
		return Transport{}, fmt.Errorf("ss plugin %q is not supported; only obfs-local with obfs=http is", name)
	}
	t := Transport{Type: "tcp"}
	for _, opt := range strings.Split(opts, ";") {
		key, value, _ := strings.Cut(opt, "=")
		switch key {
		case "obfs":
			if value != "http" {
				return Transport{}, fmt.Errorf("ss plugin obfs=%s is not supported; only obfs=http is", value)
			}
			t.HeaderType = "http"
		case "obfs-host":
			t.Host = value
		}
	}
	return t, nil
}
