package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mergemat/celare/internal/api"
	"github.com/Mergemat/celare/internal/cli"
	"github.com/Mergemat/celare/internal/daemon"
	"github.com/google/go-cmp/cmp"
)

type fakeTunnel struct{ running string }

func (f *fakeTunnel) Start(s daemon.Spec) error { f.running = s.NodeName; return nil }
func (f *fakeTunnel) Stop() error               { f.running = ""; return nil }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
func (realClock) AfterFunc(d time.Duration, f func()) func() bool {
	return time.AfterFunc(d, f).Stop
}

type okProbe struct{}

func (okProbe) Probe(context.Context, string) error { return nil }

const baseConfig = `# test config
[[node]]
name = "tokyo"
link = "trojan://s3cret-pw@203.0.113.10:443?security=tls&sni=tokyo.example.test#Tokyo"

[[subscription]]
name = "main"
url = "https://panel.example.test/sub/t0ken"

[[rule]]
name = "lan"
action = "direct"
ips = ["10.0.0.0/8"]
`

type harness struct {
	t    *testing.T
	dir  string
	sock string
	tun  *fakeTunnel
}

// newHarness writes config.toml, a cached subscription, and runs a daemon
// API with a fake tunnel.
func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "config.toml"), baseConfig)
	write(t, filepath.Join(dir, "subscriptions", "main.txt"), "hy2://auth@203.0.113.20:443?security=tls#Osaka\n")
	f, err := os.CreateTemp("/tmp", "celare-cli-*.sock")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	h := &harness{t: t, dir: dir, sock: f.Name(), tun: &fakeTunnel{}}
	srv := &api.Server{Daemon: daemon.New(h.tun, realClock{}, okProbe{}), ConfigDir: dir, StatePath: filepath.Join(dir, "state.json")}
	stop, err := srv.Listen(h.sock, os.Getuid())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stop(); os.Remove(h.sock) })
	return h
}

func write(t *testing.T, path, content string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o700)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) run(args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = cli.Run(context.Background(), args, cli.Env{
		Stdout: &out, Stderr: &errOut, ConfigDir: h.dir, Socket: h.sock,
	})
	return code, out.String(), errOut.String()
}

type envelope struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Next  []string        `json:"next"`
	Error *api.Error      `json:"error"`
}

// runJSON runs with --json and checks stdout is exactly one JSON document.
func (h *harness) runJSON(args ...string) (int, envelope) {
	h.t.Helper()
	code, stdout, _ := h.run(append(args, "--json")...)
	var env envelope
	dec := json.NewDecoder(strings.NewReader(stdout))
	if err := dec.Decode(&env); err != nil {
		h.t.Fatalf("%v: stdout is not JSON: %v\n%s", args, err, stdout)
	}
	if dec.More() {
		h.t.Fatalf("%v: stdout has more than one JSON document:\n%s", args, stdout)
	}
	return code, env
}

func (h *harness) config() string {
	b, _ := os.ReadFile(filepath.Join(h.dir, "config.toml"))
	return string(b)
}

func TestNodesListIncludesSubscriptionNodes(t *testing.T) {
	h := newHarness(t)
	code, env := h.runJSON("nodes", "list")
	var nodes []struct{ Name, Protocol, Source string }
	json.Unmarshal(env.Data, &nodes)
	got := []string{}
	for _, n := range nodes {
		got = append(got, n.Name+" "+n.Protocol+" "+n.Source)
	}
	want := []string{"tokyo trojan config", "main/Osaka hysteria2 subscription:main"}
	if code != 0 || !env.OK {
		t.Fatalf("exit %d, envelope %+v", code, env)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("nodes mismatch (-want +got):\n%s", diff)
	}
}

func TestNodesShowRedactsUnlessReveal(t *testing.T) {
	h := newHarness(t)
	_, env := h.runJSON("nodes", "show", "tokyo")
	if strings.Contains(string(env.Data), "s3cret-pw") || !strings.Contains(string(env.Data), "<redacted>") {
		t.Errorf("show leaked or did not mark the password: %s", env.Data)
	}
	_, env = h.runJSON("nodes", "show", "tokyo", "--reveal")
	if !strings.Contains(string(env.Data), "s3cret-pw") {
		t.Errorf("--reveal did not show the password: %s", env.Data)
	}
}

func TestNodesAddNamesFromTheLinkAndDryRunWritesNothing(t *testing.T) {
	h := newHarness(t)
	link := "vless://22222222-3333-4444-9555-666666666666@203.0.113.30:443?security=reality&pbk=k&sni=yahoo.com#Kyoto%20Fast"
	before := h.config()
	code, env := h.runJSON("nodes", "add", link, "--dry-run")
	if code != 0 || h.config() != before {
		t.Fatalf("dry run exit %d changed config.toml", code)
	}
	if !strings.Contains(string(env.Data), "Kyoto Fast") {
		t.Errorf("dry run does not show what would be added: %s", env.Data)
	}
	if code, _ := h.runJSON("nodes", "add", link); code != 0 {
		t.Fatalf("add exit %d", code)
	}
	if !strings.Contains(h.config(), "name = \"Kyoto Fast\"") || !strings.HasPrefix(h.config(), "# test config") {
		t.Errorf("config.toml after add:\n%s", h.config())
	}
}

func TestConnectWithConfirmTellsTheAgentHowToConfirm(t *testing.T) {
	h := newHarness(t)
	code, env := h.runJSON("connect", "main/Osaka", "--confirm", "30s")
	var c daemon.Change
	json.Unmarshal(env.Data, &c)
	if code != 0 || c.State != daemon.Pending || h.tun.running != "main/Osaka" {
		t.Fatalf("exit %d, change %+v, running %q", code, c, h.tun.running)
	}
	if len(env.Next) == 0 || !strings.HasPrefix(env.Next[0], "celare confirm "+c.ID) {
		t.Errorf("next = %v, want it to start with celare confirm %s", env.Next, c.ID)
	}
	if code, env := h.runJSON("confirm", c.ID); code != 0 || !env.OK {
		t.Errorf("confirm exit %d: %+v", code, env.Error)
	}
}

func TestExitCodes(t *testing.T) {
	h := newHarness(t)
	code, env := h.runJSON("connect", "nowhere")
	if code != 1 || env.OK || env.Error == nil || env.Error.Code != "node_not_found" {
		t.Errorf("unknown node: exit %d, error %+v; want 1 node_not_found", code, env.Error)
	}

	h.runJSON("connect", "tokyo", "--confirm", "30s")
	if code, env := h.runJSON("connect", "main/Osaka"); code != 4 || env.Error.Code != "change_pending" {
		t.Errorf("while pending: exit %d, error %+v; want 4 change_pending", code, env.Error)
	}

	if code, _, _ := h.run("connect", "--no-such-flag"); code != 2 {
		t.Errorf("unknown flag: exit %d, want 2", code)
	}

	os.Remove(h.sock)
	if code, env := h.runJSON("status"); code != 3 || env.Error.Code != "daemon_unreachable" {
		t.Errorf("no daemon: exit %d, error %+v; want 3 daemon_unreachable", code, env.Error)
	}
}

func TestRulesAddClassifiesMatches(t *testing.T) {
	h := newHarness(t)
	code, _ := h.runJSON("rules", "add", "proxy", "domain:openai.com", "203.0.113.0/24", "geoip:telegram", "--name", "ai", "--first")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	want := "[[rule]]\nname = \"ai\"\naction = \"proxy\"\ndomains = [\"domain:openai.com\"]\nips = [\"203.0.113.0/24\", \"geoip:telegram\"]\n\n[[rule]]\nname = \"lan\""
	if !strings.Contains(h.config(), want) {
		t.Errorf("config.toml after rules add:\n%s", h.config())
	}
}

func TestRulesExplainNamesTheMatchingRule(t *testing.T) {
	h := newHarness(t)
	_, env := h.runJSON("rules", "explain", "10.1.2.3")
	var got struct {
		Action string `json:"action"`
		Rule   *struct {
			Position int    `json:"position"`
			Name     string `json:"name"`
		} `json:"rule"`
	}
	json.Unmarshal(env.Data, &got)
	if got.Action != "direct" || got.Rule == nil || got.Rule.Position != 1 || got.Rule.Name != "lan" {
		t.Errorf("explain = %s, want direct by rule 1 (lan)", env.Data)
	}
	_, env = h.runJSON("rules", "explain", "203.0.113.99")
	json.Unmarshal(env.Data, &got)
	if got.Action != "proxy" || got.Rule != nil {
		t.Errorf("explain = %s, want proxy by the default", env.Data)
	}
}

func TestHumanOutputByDefault(t *testing.T) {
	h := newHarness(t)
	code, stdout, _ := h.run("status")
	if code != 0 || strings.HasPrefix(strings.TrimSpace(stdout), "{") || !strings.Contains(stdout, "down") {
		t.Errorf("status printed %q (exit %d), want a human line saying down", stdout, code)
	}
}

func TestConfigInitThenCheck(t *testing.T) {
	dir := t.TempDir()
	h := &harness{t: t, dir: dir, sock: "/nonexistent.sock"}
	if code, env := h.runJSON("config", "init"); code != 0 {
		t.Fatalf("init exit %d: %+v", code, env.Error)
	}
	if code, env := h.runJSON("config", "init"); code != 1 || env.Error.Code != "config_exists" {
		t.Errorf("second init: exit %d, error %+v; want config_exists", code, env.Error)
	}
	if code, env := h.runJSON("config", "check"); code != 0 || !env.OK {
		t.Errorf("check of a fresh config: exit %d, %+v", code, env.Error)
	}
}
