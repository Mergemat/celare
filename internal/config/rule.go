// Package config is celare's user-editable configuration: which node is
// active, how traffic is routed, and where nodes come from.
package config

import (
	"strconv"
	"strings"
)

// Action is where a rule sends matching traffic.
type Action string

const (
	Proxy  Action = "proxy"
	Direct Action = "direct"
	Block  Action = "block"
)

// Rule matches a connection to any of its Domains or IPs (they are OR-ed,
// unlike within a single Xray rule). Port and Network narrow it: the
// connection must also use them. A rule with neither Domains nor IPs
// matches every destination on its Port and Network. Domains and IPs use
// Xray's matcher syntax: "domain:example.com", "full:host", "keyword:x",
// "geosite:cn", "10.0.0.0/8", "geoip:private".
type Rule struct {
	// Name is optional; named rules can be removed by name and show up
	// by name in `celare rules explain`.
	Name    string   `toml:"name,omitempty" json:"name,omitempty"`
	Action  Action   `toml:"action" json:"action"`
	Domains []string `toml:"domains,omitempty" json:"domains,omitempty"`
	IPs     []string `toml:"ips,omitempty" json:"ips,omitempty"`
	// Network is "tcp" or "udp".
	Network string `toml:"network,omitempty" json:"network,omitempty"`
	// Port is a port, list or range in Xray's syntax: "22", "80,443", "1000-2000".
	Port string `toml:"port,omitempty" json:"port,omitempty"`
	// DNS is the server that resolves the rule's Domains, in the syntax of
	// the top-level dns list. Its queries go where the rule sends traffic,
	// so a direct rule's server sees the user's own network.
	DNS string `toml:"dns,omitempty" json:"dns,omitempty"`
}

// DefaultDNS is used when config.toml sets no dns. Lookups follow the
// routing rules like any other traffic, so by default they go through the
// proxy, encrypted.
var DefaultDNS = []string{"https://1.1.1.1/dns-query"}

// validPort reports whether s is a port, list or range Xray accepts.
func validPort(s string) bool {
	for _, part := range strings.Split(s, ",") {
		lo, hi, isRange := strings.Cut(strings.TrimSpace(part), "-")
		if !isRange {
			hi = lo
		}
		from, err1 := strconv.Atoi(lo)
		to, err2 := strconv.Atoi(hi)
		if err1 != nil || err2 != nil || from < 1 || to > 65535 || from > to {
			return false
		}
	}
	return true
}
