package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/Mergemat/celare/internal/node"
)

// Config mirrors config.toml.
type Config struct {
	Default       Action         `toml:"default" json:"default"`
	DNS           []string       `toml:"dns" json:"dns"`
	Nodes         []NodeEntry    `toml:"node" json:"nodes"`
	Subscriptions []Subscription `toml:"subscription" json:"subscriptions"`
	Rules         []Rule         `toml:"rule" json:"rules"`
}

// NodeEntry is a node the user added by hand, stored as its share link.
type NodeEntry struct {
	Name string `toml:"name" json:"name"`
	Link string `toml:"link" json:"link"`
}

type Subscription struct {
	Name string `toml:"name" json:"name"`
	URL  string `toml:"url" json:"url"`
}

// Problem is one thing wrong with a config, located by Field
// ("active", "node[2].link", ...) so the fix is unambiguous.
type Problem struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (p Problem) String() string { return p.Field + ": " + p.Message }

// Parse decodes config.toml. Unknown keys are errors.
func Parse(b []byte) (Config, error) {
	var c Config
	md, err := toml.Decode(string(b), &c)
	if err != nil {
		var perr toml.ParseError
		if errors.As(err, &perr) {
			return Config{}, fmt.Errorf("config.toml line %d: %s", perr.Position.Line, perr.Message)
		}
		return Config{}, fmt.Errorf("config.toml: %w", err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = fmt.Sprintf("%q", k.String())
		}
		return Config{}, fmt.Errorf("config.toml has unknown keys %s; see `celare help config` for the valid ones", strings.Join(keys, ", "))
	}
	return c, nil
}

// Check returns every problem in the config.
func (c Config) Check() []Problem {
	var ps []Problem
	add := func(field, format string, args ...any) {
		ps = append(ps, Problem{Field: field, Message: fmt.Sprintf(format, args...)})
	}
	switch c.Default {
	case "", Proxy, Direct, Block:
	default:
		add("default", "%q is not one of proxy, direct, block", c.Default)
	}
	seen := map[string]bool{}
	for i, n := range c.Nodes {
		field := fmt.Sprintf("node[%d]", i+1)
		switch {
		case n.Name == "":
			add(field+".name", "is empty")
		case strings.Contains(n.Name, "/"):
			add(field+".name", "%q contains '/', which is reserved for <subscription>/<node>", n.Name)
		case seen[n.Name]:
			add(field+".name", "%q is used by an earlier node", n.Name)
		}
		seen[n.Name] = true
		if _, err := node.ParseLink(n.Link); err != nil {
			add(field+".link", "%v", err)
		}
	}
	ruleNames := map[string]bool{}
	for i, r := range c.Rules {
		field := fmt.Sprintf("rule[%d]", i+1)
		if r.Name != "" {
			if _, err := strconv.Atoi(r.Name); err == nil {
				add(field+".name", "%q is a number, which would be mistaken for a position", r.Name)
			} else if ruleNames[r.Name] {
				add(field+".name", "%q is used by an earlier rule", r.Name)
			}
			ruleNames[r.Name] = true
		}
		switch r.Action {
		case Proxy, Direct, Block:
		default:
			add(field+".action", "%q is not one of proxy, direct, block", r.Action)
			continue
		}
		if len(r.Domains) == 0 && len(r.IPs) == 0 {
			add(field, "has no domains or ips, so it matches nothing")
		}
	}
	return ps
}
