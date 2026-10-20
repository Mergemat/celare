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

const configTOML = `active = "tokyo"

[[node]]
name = "tokyo"
link = "trojan://pw@203.0.113.10:443?security=tls#Tokyo"

[[node]]
name = "osaka"
link = "trojan://pw@203.0.113.11:443?security=tls#Osaka"

[[rule]]
action = "direct"
ips = ["192.168.0.0/16"]
`

// serve runs the API on a unix socket in a temp dir and returns a client.
func serve(t *testing.T, toml string) (*api.Client, *fakeTunnel, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	tun := &fakeTunnel{}
	srv := &api.Server{
		Daemon:    daemon.New(tun, realClock{}, okProbe{}),
		ConfigDir: dir,
		StatePath: filepath.Join(dir, "state.json"),
	}
	// Unix socket paths are limited to ~104 bytes on macOS; TempDir can
	// be longer, so the socket goes in /tmp.
	sock, err := os.CreateTemp("/tmp", "celare-*.sock")
	if err != nil {
		t.Fatal(err)
	}
	sock.Close()
	os.Remove(sock.Name())
	stop, err := srv.Listen(sock.Name(), os.Getuid())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stop(); os.Remove(sock.Name()) })
	return api.NewClient(sock.Name()), tun, dir
}

func TestUpUsesTheActiveNodeAndReportsStatus(t *testing.T) {
	c, tun, _ := serve(t, configTOML)
	st, err := c.Up(context.Background(), api.ChangeRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if tun.running != "tokyo" || st.State != "up" || st.Node != "tokyo" {
		t.Errorf("running %q, status %+v; want tokyo up", tun.running, st)
	}
}

func TestUpNamedNodeWithConfirmIsPending(t *testing.T) {
	c, tun, _ := serve(t, configTOML)
	st, err := c.Up(context.Background(), api.ChangeRequest{Node: "osaka", Confirm: "30s"})
	if err != nil {
		t.Fatal(err)
	}
	if tun.running != "osaka" || st.Pending == nil || st.Pending.RevertsTo != "down" {
		t.Errorf("running %q, pending %+v; want osaka pending, reverting to down", tun.running, st.Pending)
	}
	if _, err := c.Confirm(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestErrorsCarryACodeAndAHint(t *testing.T) {
	c, _, _ := serve(t, configTOML)
	_, err := c.Up(context.Background(), api.ChangeRequest{Node: "nowhere"})
	var apiErr *api.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "node_not_found" || apiErr.Hint == "" {
		t.Fatalf("err = %#v, want code node_not_found with a hint", err)
	}

	if _, err := c.Up(context.Background(), api.ChangeRequest{Node: "tokyo", Confirm: "30s"}); err != nil {
		t.Fatal(err)
	}
	_, err = c.Up(context.Background(), api.ChangeRequest{Node: "osaka"})
	if !errors.As(err, &apiErr) || apiErr.Code != "change_pending" {
		t.Fatalf("err = %#v, want code change_pending", err)
	}
}

func TestInvalidConfigIsRefusedWithEveryProblem(t *testing.T) {
	c, tun, _ := serve(t, "active = \"tokyo\"\ndefault = \"sideways\"\n")
	_, err := c.Up(context.Background(), api.ChangeRequest{})
	var apiErr *api.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "config_invalid" || len(apiErr.Problems) != 2 {
		t.Fatalf("err = %#v, want config_invalid with 2 problems", err)
	}
	if tun.running != "" {
		t.Errorf("tunnel started with an invalid config")
	}
}

// After a reboot the daemon brings back what was last committed.
func TestCommittedStateSurvivesARestart(t *testing.T) {
	c, _, dir := serve(t, configTOML)
	if _, err := c.Up(context.Background(), api.ChangeRequest{Node: "osaka"}); err != nil {
		t.Fatal(err)
	}
	tun := &fakeTunnel{}
	srv := &api.Server{
		Daemon:    daemon.New(tun, realClock{}, okProbe{}),
		ConfigDir: dir,
		StatePath: filepath.Join(dir, "state.json"),
	}
	if err := srv.Resume(context.Background()); err != nil {
		t.Fatal(err)
	}
	if tun.running != "osaka" {
		t.Errorf("after resume running %q, want osaka", tun.running)
	}
}
