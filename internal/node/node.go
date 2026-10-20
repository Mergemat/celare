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
	Transport   Transport
	Security    Security
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
	Type string
	Path string
	Host string
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
	n := Node{
		Name:     u.Fragment,
		Protocol: u.Scheme,
		Address:  u.Hostname(),
		Port:     port,
		Transport: Transport{
			Type: q.Get("type"),
			Path: q.Get("path"),
			Host: q.Get("host"),
		},
		Security: Security{
			Type:        q.Get("security"),
			SNI:         q.Get("sni"),
			Fingerprint: q.Get("fp"),
			ALPN:        splitList(q.Get("alpn")),
			ECHConfig:   q.Get("ech"),
			PublicKey:   q.Get("pbk"),
			ShortID:     q.Get("sid"),
			SpiderX:     q.Get("spx"),
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
		}
		if mtu := q.Get("mtu"); mtu != "" {
			if n.WireGuard.MTU, err = strconv.Atoi(mtu); err != nil {
				return Node{}, fmt.Errorf("wireguard link has invalid mtu %q", mtu)
			}
		}
	case "ss":
		n.Protocol = "shadowsocks"
		n.Cipher, n.Password, err = shadowsocksCredentials(u.User)
		if err != nil {
			return Node{}, err
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
	return Node{
		Name:     v.Name,
		Protocol: "vmess",
		Address:  v.Address,
		Port:     port,
		UUID:     v.ID,
		Cipher:   v.Cipher,
		Transport: Transport{
			Type: v.Network,
			Path: v.Path,
			Host: v.Host,
		},
		Security: Security{
			Type:        v.Security,
			SNI:         v.SNI,
			Fingerprint: v.FP,
			ALPN:        splitList(v.ALPN),
		},
	}, nil
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
