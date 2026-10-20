// Package config is celare's user-editable configuration: which node is
// active, how traffic is routed, and where nodes come from.
package config

// Action is where a rule sends matching traffic.
type Action string

const (
	Proxy  Action = "proxy"
	Direct Action = "direct"
	Block  Action = "block"
)

// Rule matches traffic by any of its conditions (they are OR-ed, unlike
// within a single Xray rule). Domains and IPs use Xray's matcher syntax:
// "domain:example.com", "full:host", "keyword:x", "geosite:cn",
// "10.0.0.0/8", "geoip:private".
type Rule struct {
	// Name is optional; named rules can be removed by name and show up
	// by name in `celare rules explain`.
	Name    string   `toml:"name,omitempty" json:"name,omitempty"`
	Action  Action   `toml:"action" json:"action"`
	Domains []string `toml:"domains,omitempty" json:"domains,omitempty"`
	IPs     []string `toml:"ips,omitempty" json:"ips,omitempty"`
}

// DefaultDNS is used when config.toml sets no dns. Lookups follow the
// routing rules like any other traffic, so by default they go through the
// proxy, encrypted.
var DefaultDNS = []string{"https://1.1.1.1/dns-query"}
