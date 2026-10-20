package node

import (
	"bytes"
	"strings"
)

// LineProblem is a subscription line that could not be parsed. Line is
// 1-based within the decoded body.
type LineProblem struct {
	Line int
	Err  error
}

// ParseSubscription reads a subscription body: newline-separated share
// links, either plain or base64-encoded as a whole (the 3x-ui default).
// Unparseable lines are reported instead of failing the whole body.
func ParseSubscription(body []byte) ([]Node, []LineProblem) {
	text := string(bytes.TrimSpace(body))
	if !strings.Contains(text, "://") {
		if decoded, err := decodeBase64(text); err == nil {
			text = string(decoded)
		}
	}
	var nodes []Node
	var problems []LineProblem
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		n, err := ParseLink(line)
		if err != nil {
			problems = append(problems, LineProblem{Line: i + 1, Err: err})
			continue
		}
		nodes = append(nodes, n)
	}
	return nodes, problems
}
