package config

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// The CLI edits config.toml line by line instead of re-encoding it, so
// comments and layout the user wrote survive. Every edit is re-parsed
// before it is written.

var (
	tableHeader = regexp.MustCompile(`^\s*\[`)
	nameLine    = regexp.MustCompile(`^\s*name\s*=\s*(".*")\s*(#.*)?$`)
)

// AddNode appends a [[node]] table.
func AddNode(path string, n NodeEntry) error {
	return edit(path, func(lines []string) ([]string, error) {
		if _, _, ok := findTable(lines, "node", n.Name); ok {
			return nil, fmt.Errorf("a node named %q already exists; pick another name or remove it with `celare nodes remove %s`", n.Name, n.Name)
		}
		return appendTable(lines, "[[node]]", "name = "+quote(n.Name), "link = "+quote(n.Link)), nil
	})
}

// RemoveNode deletes the [[node]] table with the given name.
func RemoveNode(path, name string) error {
	return edit(path, func(lines []string) ([]string, error) {
		start, end, ok := findTable(lines, "node", name)
		if !ok {
			return nil, fmt.Errorf("no node named %q in config.toml; run `celare nodes list` for the names", name)
		}
		return removeRange(lines, start, end), nil
	})
}

// AddRule adds a [[rule]] table, before every other rule when first is
// set (rules match top to bottom), otherwise after them.
func AddRule(path string, r Rule, first bool) error {
	return edit(path, func(lines []string) ([]string, error) {
		if r.Name != "" {
			if _, _, ok := findTable(lines, "rule", r.Name); ok {
				return nil, fmt.Errorf("a rule named %q already exists", r.Name)
			}
		}
		body := []string{"[[rule]]"}
		if r.Name != "" {
			body = append(body, "name = "+quote(r.Name))
		}
		body = append(body, "action = "+quote(string(r.Action)))
		if len(r.Domains) > 0 {
			body = append(body, "domains = "+quoteList(r.Domains))
		}
		if len(r.IPs) > 0 {
			body = append(body, "ips = "+quoteList(r.IPs))
		}
		existing := tables(lines, "rule")
		if !first || len(existing) == 0 {
			return appendTable(lines, body[0], body[1:]...), nil
		}
		return insert(lines, existing[0].start, append(body, "")...), nil
	})
}

// RemoveRule deletes a rule given its name or its 1-based position.
func RemoveRule(path, ref string) error {
	return edit(path, func(lines []string) ([]string, error) {
		rules := tables(lines, "rule")
		if pos, err := strconv.Atoi(ref); err == nil {
			if pos < 1 || pos > len(rules) {
				return nil, fmt.Errorf("there is no rule %d (config.toml has %d); run `celare rules list` for positions and names", pos, len(rules))
			}
			t := rules[pos-1]
			return removeRange(lines, t.start, t.end), nil
		}
		start, end, ok := findTable(lines, "rule", ref)
		if !ok {
			return nil, fmt.Errorf("no rule named %q; run `celare rules list` for positions and names", ref)
		}
		return removeRange(lines, start, end), nil
	})
}

// AddSubscription appends a [[subscription]] table.
func AddSubscription(path string, s Subscription) error {
	return edit(path, func(lines []string) ([]string, error) {
		if _, _, ok := findTable(lines, "subscription", s.Name); ok {
			return nil, fmt.Errorf("a subscription named %q already exists", s.Name)
		}
		return appendTable(lines, "[[subscription]]", "name = "+quote(s.Name), "url = "+quote(s.URL)), nil
	})
}

// RemoveSubscription deletes the [[subscription]] table with the given name.
func RemoveSubscription(path, name string) error {
	return edit(path, func(lines []string) ([]string, error) {
		start, end, ok := findTable(lines, "subscription", name)
		if !ok {
			return nil, fmt.Errorf("no subscription named %q in config.toml; run `celare subs list` for the names", name)
		}
		return removeRange(lines, start, end), nil
	})
}

// table is the line range of one [[kind]] table. start includes the
// comment lines directly above the header, which describe it; end stops
// before comments that describe the next table but includes the blank
// lines separating the two.
type table struct{ start, header, end int }

func tables(lines []string, kind string) []table {
	header := "[[" + kind + "]]"
	var out []table
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != header {
			continue
		}
		next := len(lines)
		for j := i + 1; j < len(lines); j++ {
			if tableHeader.MatchString(lines[j]) {
				next = j
				break
			}
		}
		start := i
		for start > 0 && isComment(lines[start-1]) {
			start--
		}
		end := next
		for end > i+1 && isBlankOrComment(lines[end-1]) {
			end--
		}
		for end < next && strings.TrimSpace(lines[end]) == "" {
			end++
		}
		out = append(out, table{start: start, header: i, end: end})
		i = next - 1
	}
	return out
}

// findTable locates the [[kind]] table whose name is name.
func findTable(lines []string, kind, name string) (start, end int, ok bool) {
	for _, t := range tables(lines, kind) {
		if tableNamed(lines[t.header+1:t.end], name) {
			return t.start, t.end, true
		}
	}
	return 0, 0, false
}

// removeRange deletes lines[start:end] and, when that leaves two blank
// lines in a row or a blank line at the end, the extra blank line too.
func removeRange(lines []string, start, end int) []string {
	out := append(lines[:start:start], lines[end:]...)
	if start > 0 && start <= len(out) && strings.TrimSpace(out[start-1]) == "" &&
		(start == len(out) || strings.TrimSpace(out[start]) == "") {
		out = append(out[:start-1], out[start:]...)
	}
	return out
}

func tableNamed(body []string, name string) bool {
	for _, l := range body {
		if m := nameLine.FindStringSubmatch(l); m != nil {
			var v struct{ V string }
			if _, err := decodeValue(m[1], &v); err == nil {
				return v.V == name
			}
		}
	}
	return false
}

func isComment(l string) bool {
	return strings.HasPrefix(strings.TrimSpace(l), "#")
}

func isBlankOrComment(l string) bool {
	t := strings.TrimSpace(l)
	return t == "" || strings.HasPrefix(t, "#")
}

func appendTable(lines []string, header string, body ...string) []string {
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > 0 {
		lines = append(lines, "")
	}
	return append(append(lines, header), body...)
}

func insert(lines []string, at int, add ...string) []string {
	out := make([]string, 0, len(lines)+len(add))
	out = append(out, lines[:at]...)
	out = append(out, add...)
	return append(out, lines[at:]...)
}

func edit(path string, change func([]string) ([]string, error)) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := strings.TrimSuffix(string(b), "\n")
	var lines []string
	if text != "" {
		lines = strings.Split(text, "\n")
	}
	lines, err = change(lines)
	if err != nil {
		return err
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	out := strings.Join(lines, "\n") + "\n"
	if _, err := Parse([]byte(out)); err != nil {
		return fmt.Errorf("edit would leave config.toml invalid, nothing written: %w", err)
	}
	return writeAtomic(path, []byte(out))
}

func writeAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
