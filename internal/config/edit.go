package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// The CLI edits config.toml line by line instead of re-encoding it, so
// comments and layout the user wrote survive. Every edit is re-parsed
// before it is written.

var (
	tableHeader = regexp.MustCompile(`^\s*\[`)
	activeLine  = regexp.MustCompile(`^\s*active\s*=`)
	nameLine    = regexp.MustCompile(`^\s*name\s*=\s*(".*")\s*(#.*)?$`)
)

// SetActive sets the top-level active key.
func SetActive(path, name string) error {
	return edit(path, func(lines []string) ([]string, error) {
		line := "active = " + quote(name)
		for i, l := range lines {
			if tableHeader.MatchString(l) {
				break
			}
			if activeLine.MatchString(l) {
				lines[i] = line
				return lines, nil
			}
		}
		at := len(lines)
		for i, l := range lines {
			if tableHeader.MatchString(l) {
				at = i
				break
			}
		}
		return insert(lines, at, line, ""), nil
	})
}

// AddNode appends a [[node]] table.
func AddNode(path string, n NodeEntry) error {
	return edit(path, func(lines []string) ([]string, error) {
		if _, _, ok := findTable(lines, "node", n.Name); ok {
			return nil, fmt.Errorf("a node named %q already exists; pick another name or remove it with `celare node rm %s`", n.Name, n.Name)
		}
		return appendTable(lines, "[[node]]", "name = "+quote(n.Name), "link = "+quote(n.Link)), nil
	})
}

// RemoveNode deletes the [[node]] table with the given name.
func RemoveNode(path, name string) error {
	return edit(path, func(lines []string) ([]string, error) {
		start, end, ok := findTable(lines, "node", name)
		if !ok {
			return nil, fmt.Errorf("no node named %q in config.toml; run `celare node list` for the names", name)
		}
		return append(lines[:start:start], lines[end:]...), nil
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
			return nil, fmt.Errorf("no subscription named %q in config.toml; run `celare sub list` for the names", name)
		}
		return append(lines[:start:start], lines[end:]...), nil
	})
}

// findTable locates the [[kind]] table whose name is name. end excludes
// trailing comments, which belong to the table after it, but includes the
// blank lines before them.
func findTable(lines []string, kind, name string) (start, end int, ok bool) {
	header := "[[" + kind + "]]"
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
		if !tableNamed(lines[i+1:next], name) {
			i = next - 1
			continue
		}
		end = next
		for end > i+1 && isBlankOrComment(lines[end-1]) {
			end--
		}
		// Keep the comments attached to the next table, drop the blank
		// lines that separated this one.
		for end < next && strings.TrimSpace(lines[end]) == "" {
			end++
		}
		return i, end, true
	}
	return 0, 0, false
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
