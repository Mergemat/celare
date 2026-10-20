package node_test

import (
	"testing"

	"github.com/b-omarov/celare/internal/node"
	"github.com/google/go-cmp/cmp"
)

// A bad line must not cost the user the rest of the subscription: the
// good nodes come back and the bad line is reported by number.
func TestParseSubscriptionBase64KeepsGoodLines(t *testing.T) {
	body := []byte("dHJvamFuOi8vdHJvamFuLXRlc3QtcHctWFlaQGV4YW1wbGUudGVzdDo0NDM/dHlwZT13cyZwYXRoPSUyRnRyb2phbiZob3N0PXRyb2phbi5leGFtcGxlLnRlc3Qmc2VjdXJpdHk9dGxzI29uZQpub3QtYS1saW5rCmh5MjovL2F1dGhAZXhhbXBsZS50ZXN0OjQ0Mz9zZWN1cml0eT10bHMmc25pPWV4YW1wbGUudGVzdCN0d28K\n")
	nodes, problems := node.ParseSubscription(body)

	var names []string
	for _, n := range nodes {
		names = append(names, n.Name)
	}
	if diff := cmp.Diff([]string{"one", "two"}, names); diff != "" {
		t.Errorf("node names mismatch (-want +got):\n%s", diff)
	}
	if len(problems) != 1 || problems[0].Line != 2 {
		t.Fatalf("problems = %+v, want one problem on line 2", problems)
	}
}

func TestParseSubscriptionPlainText(t *testing.T) {
	body := []byte("hy2://auth@example.test:443?security=tls#plain\r\n")
	nodes, problems := node.ParseSubscription(body)
	if len(problems) != 0 || len(nodes) != 1 || nodes[0].Name != "plain" {
		t.Fatalf("nodes = %+v, problems = %+v; want one node named plain", nodes, problems)
	}
}
