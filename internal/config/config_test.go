package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mergemat/celare/internal/config"
	"github.com/google/go-cmp/cmp"
)

const sample = `# my config
active = "tokyo"
default = "proxy"
dns = ["https://1.1.1.1/dns-query"]

[[node]]
name = "tokyo"
link = "trojan://pw@203.0.113.10:8444?security=tls&sni=203.0.113.10#Tokyo"

[[subscription]]
name = "main"
url = "https://panel.example.test/sub/abc"

# LAN stays local
[[rule]]
action = "direct"
ips = ["192.168.0.0/16"]

[[rule]]
action = "block"
domains = ["domain:ads.example.test"]
`

func TestParse(t *testing.T) {
	got, err := config.Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	want := config.Config{
		Active:        "tokyo",
		Default:       config.Proxy,
		DNS:           []string{"https://1.1.1.1/dns-query"},
		Nodes:         []config.NodeEntry{{Name: "tokyo", Link: "trojan://pw@203.0.113.10:8444?security=tls&sni=203.0.113.10#Tokyo"}},
		Subscriptions: []config.Subscription{{Name: "main", URL: "https://panel.example.test/sub/abc"}},
		Rules: []config.Rule{
			{Action: config.Direct, IPs: []string{"192.168.0.0/16"}},
			{Action: config.Block, Domains: []string{"domain:ads.example.test"}},
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Parse mismatch (-want +got):\n%s", diff)
	}
}

func TestParseReportsTheLineOfASyntaxError(t *testing.T) {
	_, err := config.Parse([]byte("active = \"x\"\ndefault = proxy\n"))
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("err = %v, want a message naming line 2", err)
	}
}

func TestCheckFindsEveryProblem(t *testing.T) {
	cfg := config.Config{
		Active:  "nowhere",
		Default: "sideways",
		Nodes: []config.NodeEntry{
			{Name: "a", Link: "trojan://pw@example.test:443#a"},
			{Name: "a", Link: "nonsense"},
		},
		Rules: []config.Rule{{Action: "maybe"}, {Action: config.Direct}},
	}
	var got []string
	for _, p := range cfg.Check(nil) {
		got = append(got, p.Field)
	}
	want := []string{"active", "default", "node[2].name", "node[2].link", "rule[1].action", "rule[2]"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("problem fields mismatch (-want +got):\n%s", diff)
	}
}

func TestSetActiveKeepsComments(t *testing.T) {
	path := writeFile(t, sample)
	if err := config.SetActive(path, "main/Osaka"); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(sample, `active = "tokyo"`, `active = "main/Osaka"`, 1)
	assertFile(t, path, want)
}

func TestSetActiveAddsTheKeyBeforeTheFirstTable(t *testing.T) {
	path := writeFile(t, "# hi\n\n[[node]]\nname = \"a\"\nlink = \"trojan://pw@example.test:443#a\"\n")
	if err := config.SetActive(path, "a"); err != nil {
		t.Fatal(err)
	}
	assertFile(t, path, "# hi\n\nactive = \"a\"\n\n[[node]]\nname = \"a\"\nlink = \"trojan://pw@example.test:443#a\"\n")
}

func TestAddAndRemoveNodeKeepComments(t *testing.T) {
	path := writeFile(t, sample)
	if err := config.AddNode(path, config.NodeEntry{Name: "osaka", Link: "hy2://auth@example.test:443?security=tls#Osaka"}); err != nil {
		t.Fatal(err)
	}
	added := sample + "\n[[node]]\nname = \"osaka\"\nlink = \"hy2://auth@example.test:443?security=tls#Osaka\"\n"
	assertFile(t, path, added)

	if err := config.RemoveNode(path, "tokyo"); err != nil {
		t.Fatal(err)
	}
	removed := strings.Replace(added, "[[node]]\nname = \"tokyo\"\nlink = \"trojan://pw@203.0.113.10:8444?security=tls&sni=203.0.113.10#Tokyo\"\n\n", "", 1)
	assertFile(t, path, removed)
}

func TestRemoveNodeRejectsUnknownName(t *testing.T) {
	path := writeFile(t, sample)
	err := config.RemoveNode(path, "nope")
	if err == nil || !strings.Contains(err.Error(), "celare node list") {
		t.Fatalf("err = %v, want a hint to run celare node list", err)
	}
	assertFile(t, path, sample)
}

func writeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(want, string(got)); diff != "" {
		t.Errorf("file mismatch (-want +got):\n%s", diff)
	}
}

// A typo must not be silently ignored: "dsn" would otherwise leave DNS
// at its default with no hint why.
func TestParseRejectsUnknownKeys(t *testing.T) {
	_, err := config.Parse([]byte("dsn = [\"1.1.1.1\"]\n"))
	if err == nil || !strings.Contains(err.Error(), `"dsn"`) {
		t.Fatalf("err = %v, want it to name the unknown key dsn", err)
	}
}
