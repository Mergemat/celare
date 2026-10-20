package api_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mergemat/celare/internal/api"
	"github.com/Mergemat/celare/internal/daemon"
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

const configTOML = `[[node]]
name = "tokyo"
link = "trojan://pw@203.0.113.10:443?security=tls#Tokyo"

[[node]]
name = "osaka"
link = "trojan://pw@203.0.113.11:443?security=tls#Osaka"

[[rule]]
name = "lan"
action = "direct"
ips = ["192.168.0.0/16"]
`

type env struct {
	client *api.Client
	tun    *fakeTunnel
	dir    string
	sock   string
}

// serve runs the API on a unix socket and returns a client for it.
func serve(t *testing.T, toml string) env {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	// Unix socket paths are limited to 104 bytes on macOS and TempDir
	// can be longer, so the socket goes in /tmp.
	f, err := os.CreateTemp("/tmp", "celare-*.sock")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	e := env{tun: &fakeTunnel{}, dir: dir, sock: f.Name()}
	e.client = start(t, e)
	return e
}

func start(t *testing.T, e env) *api.Client {
	t.Helper()
	srv := &api.Server{
		Daemon:    daemon.New(e.tun, realClock{}, okProbe{}),
		ConfigDir: e.dir,
		StatePath: filepath.Join(e.dir, "state.json"),
	}
	if err := srv.Resume(context.Background()); err != nil {
		t.Fatal(err)
	}
	stop, err := srv.Listen(e.sock, os.Getuid())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stop(); os.Remove(e.sock) })
	return api.NewClient(e.sock)
}

var ctx = context.Background()

func TestConnectThenTunnelShowsTheNode(t *testing.T) {
	e := serve(t, configTOML)
	c, err := e.client.CreateChange(ctx, api.ChangeRequest{Action: "connect", Node: "tokyo"})
	if err != nil {
		t.Fatal(err)
	}
	if c.State != daemon.Committed || e.tun.running != "tokyo" {
		t.Errorf("change %+v, running %q; want committed on tokyo", c, e.tun.running)
	}
	tun, err := e.client.Tunnel(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if tun.State != "up" || tun.Node != "tokyo" {
		t.Errorf("tunnel = %+v, want up on tokyo", tun)
	}
}

func TestConnectWithoutANodeReconnectsTheLastOne(t *testing.T) {
	e := serve(t, configTOML)
	mustChange(t, e.client, api.ChangeRequest{Action: "connect", Node: "osaka"})
	mustChange(t, e.client, api.ChangeRequest{Action: "disconnect"})
	tun, err := e.client.Tunnel(ctx)
	if err != nil || tun.LastNode != "osaka" {
		t.Fatalf("tunnel = %+v, %v; want lastNode osaka while down", tun, err)
	}
	mustChange(t, e.client, api.ChangeRequest{Action: "connect"})
	if e.tun.running != "osaka" {
		t.Errorf("running %q, want osaka again", e.tun.running)
	}
}

func TestPendingChangeIsConfirmedByID(t *testing.T) {
	e := serve(t, configTOML)
	c := mustChange(t, e.client, api.ChangeRequest{Action: "connect", Node: "osaka", Confirm: "30s"})
	if c.State != daemon.Pending || c.Deadline == nil {
		t.Fatalf("change = %+v, want pending with a deadline", c)
	}
	got, err := e.client.Confirm(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != daemon.Committed {
		t.Errorf("after confirm state %s, want committed", got.State)
	}
	listed, err := e.client.Changes(ctx)
	if err != nil || len(listed) != 1 || listed[0].ID != c.ID {
		t.Errorf("changes = %+v, %v; want just %s", listed, err, c.ID)
	}
}

func TestDryRunReturnsThePlan(t *testing.T) {
	e := serve(t, configTOML)
	c := mustChange(t, e.client, api.ChangeRequest{Action: "connect", Node: "tokyo", DryRun: true})
	if c.State != daemon.Planned || c.Diff.Node == nil || c.Diff.Node.To != "tokyo" || e.tun.running != "" {
		t.Errorf("change %+v, running %q; want a plan to bring up tokyo and nothing running", c, e.tun.running)
	}
}

func TestErrorsCarryACodeAndAHint(t *testing.T) {
	e := serve(t, configTOML)
	cases := []struct {
		req  api.ChangeRequest
		code string
	}{
		{api.ChangeRequest{Action: "connect", Node: "nowhere"}, "node_not_found"},
		{api.ChangeRequest{Action: "connect"}, "no_node"},
		{api.ChangeRequest{Action: "apply"}, "tunnel_down"},
		{api.ChangeRequest{Action: "teleport"}, "bad_request"},
		{api.ChangeRequest{Action: "connect", Node: "tokyo", Confirm: "soon"}, "bad_request"},
	}
	for _, tc := range cases {
		_, err := e.client.CreateChange(ctx, tc.req)
		var apiErr *api.Error
		if !errors.As(err, &apiErr) || apiErr.Code != tc.code {
			t.Errorf("%+v: err = %v, want code %s", tc.req, err, tc.code)
		}
	}

	pending := mustChange(t, e.client, api.ChangeRequest{Action: "connect", Node: "tokyo", Confirm: "30s"})
	_, err := e.client.CreateChange(ctx, api.ChangeRequest{Action: "connect", Node: "osaka"})
	var apiErr *api.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "change_pending" || apiErr.Hint == "" {
		t.Errorf("err = %v, want change_pending with a hint", err)
	}
	if _, err := e.client.Revert(ctx, pending.ID); err != nil {
		t.Fatal(err)
	}
	_, err = e.client.Confirm(ctx, pending.ID)
	if !errors.As(err, &apiErr) || apiErr.Code != "not_pending" {
		t.Errorf("err = %v, want not_pending", err)
	}
}

func TestInvalidConfigIsRefusedWithEveryProblem(t *testing.T) {
	e := serve(t, configTOML+"\n[[rule]]\naction = \"sideways\"\n\n[[rule]]\naction = \"direct\"\n")
	_, err := e.client.CreateChange(ctx, api.ChangeRequest{Action: "connect", Node: "tokyo"})
	var apiErr *api.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "config_invalid" || len(apiErr.Problems) != 2 {
		t.Fatalf("err = %v, want config_invalid with 2 problems", err)
	}
	if e.tun.running != "" {
		t.Errorf("tunnel started with an invalid config")
	}
}

// After a reboot the daemon brings back what was last committed.
func TestCommittedStateSurvivesARestart(t *testing.T) {
	e := serve(t, configTOML)
	mustChange(t, e.client, api.ChangeRequest{Action: "connect", Node: "osaka"})
	restarted := env{tun: &fakeTunnel{}, dir: e.dir, sock: e.sock + "2"}
	start(t, restarted)
	if restarted.tun.running != "osaka" {
		t.Errorf("after restart running %q, want osaka", restarted.tun.running)
	}
}

func TestEventsSinceACursor(t *testing.T) {
	e := serve(t, configTOML)
	mustChange(t, e.client, api.ChangeRequest{Action: "connect", Node: "tokyo"})
	events, err := e.client.Events(ctx, 0)
	if err != nil || len(events) != 1 || events[0].Kind != "change.committed" {
		t.Fatalf("events = %+v, %v; want one change.committed", events, err)
	}
	later, err := e.client.Events(ctx, events[0].Seq)
	if err != nil || len(later) != 0 {
		t.Errorf("events after the cursor = %+v, %v; want none", later, err)
	}
}

func mustChange(t *testing.T, c *api.Client, r api.ChangeRequest) daemon.Change {
	t.Helper()
	ch, err := c.CreateChange(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

type failingProbe struct{}

func (failingProbe) Probe(context.Context, string) error { return errors.New("no response within 15s") }

// When nothing gets through, the error says why: the server tests
// the node outside the tunnel and puts the verdict in the hint.
func TestFailedTrafficCheckCarriesTheDiagnosis(t *testing.T) {
	e := serve(t, configTOML)
	srv := &api.Server{
		Daemon:    daemon.New(&fakeTunnel{}, realClock{}, failingProbe{}),
		ConfigDir: e.dir,
		StatePath: filepath.Join(e.dir, "state2.json"),
		Diagnose: func(_ context.Context, spec *daemon.Spec) string {
			return spec.NodeName + " is blocked on this network"
		},
	}
	sock := e.sock + "d"
	stop, err := srv.Listen(sock, os.Getuid())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stop(); os.Remove(sock) })

	_, err = api.NewClient(sock).CreateChange(ctx, api.ChangeRequest{Action: "connect", Node: "tokyo", Probe: "https://example.test"})
	var apiErr *api.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "probe_failed" || apiErr.Hint != "tokyo is blocked on this network" {
		t.Fatalf("err = %#v, want probe_failed with the diagnosis as hint", err)
	}
	if apiErr.Change == nil || apiErr.Change.State != daemon.Reverted {
		t.Errorf("change = %+v, want the reverted change attached", apiErr.Change)
	}
}
