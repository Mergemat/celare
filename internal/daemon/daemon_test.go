package daemon_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mergemat/celare/internal/daemon"
	"github.com/Mergemat/celare/internal/node"
)

// fakeTunnel records what is running instead of touching the network.
type fakeTunnel struct {
	mu      sync.Mutex
	running string // node name, "" when down
	failFor string // Start fails for this node
}

func (f *fakeTunnel) Start(s daemon.Spec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s.NodeName == f.failFor {
		return errors.New("utun refused")
	}
	f.running = s.NodeName
	return nil
}

func (f *fakeTunnel) Stop() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.running = ""
	return nil
}

func (f *fakeTunnel) Running() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running
}

// fakeClock fires timers only when the test says so.
type fakeClock struct {
	now   time.Time
	fires []func()
}

func (c *fakeClock) Now() time.Time { return c.now }
func (c *fakeClock) AfterFunc(d time.Duration, f func()) func() bool {
	i := len(c.fires)
	c.fires = append(c.fires, f)
	return func() bool { c.fires[i] = nil; return true }
}
func (c *fakeClock) fireAll() {
	for _, f := range c.fires {
		if f != nil {
			f()
		}
	}
}

type probeFunc func(ctx context.Context, url string) error

func (p probeFunc) Probe(ctx context.Context, url string) error { return p(ctx, url) }

func spec(name string) daemon.Spec {
	return daemon.Spec{NodeName: name, Node: node.Node{Name: name}}
}

func newDaemon(t *testing.T) (*daemon.Daemon, *fakeTunnel, *fakeClock) {
	t.Helper()
	tun := &fakeTunnel{}
	clock := &fakeClock{now: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}
	d := daemon.New(tun, clock, probeFunc(func(context.Context, string) error { return nil }))
	return d, tun, clock
}

func TestUpWithoutConfirmStaysUp(t *testing.T) {
	d, tun, clock := newDaemon(t)
	if err := d.Apply(context.Background(), daemon.Change{Spec: ptr(spec("tokyo"))}); err != nil {
		t.Fatal(err)
	}
	clock.fireAll()
	if tun.Running() != "tokyo" {
		t.Errorf("running %q, want tokyo", tun.Running())
	}
	if st := d.Status(); st.State != "up" || st.Node != "tokyo" || st.Pending != nil {
		t.Errorf("status = %+v, want up on tokyo with nothing pending", st)
	}
}

// The agent switches nodes, loses its connection, and never confirms:
// the daemon must put the old node back on its own.
func TestUnconfirmedChangeRollsBack(t *testing.T) {
	d, tun, clock := newDaemon(t)
	mustApply(t, d, daemon.Change{Spec: ptr(spec("tokyo"))})
	mustApply(t, d, daemon.Change{Spec: ptr(spec("osaka")), ConfirmWithin: 30 * time.Second})

	st := d.Status()
	if st.Pending == nil || !st.Pending.Deadline.Equal(clock.now.Add(30*time.Second)) || st.Pending.RevertsTo != "tokyo" {
		t.Fatalf("pending = %+v, want a 30s deadline reverting to tokyo", st.Pending)
	}
	clock.fireAll()
	if tun.Running() != "tokyo" {
		t.Errorf("after the deadline running %q, want tokyo back", tun.Running())
	}
	st = d.Status()
	if st.Pending != nil || st.Node != "tokyo" {
		t.Errorf("status after rollback = %+v, want tokyo with nothing pending", st)
	}
	if st.LastEvent == nil || !strings.Contains(st.LastEvent.Message, "not confirmed") {
		t.Errorf("last event = %+v, want it to say the change was not confirmed", st.LastEvent)
	}
}

func TestConfirmKeepsTheChange(t *testing.T) {
	d, tun, clock := newDaemon(t)
	mustApply(t, d, daemon.Change{Spec: ptr(spec("osaka")), ConfirmWithin: 30 * time.Second})
	if err := d.Confirm(); err != nil {
		t.Fatal(err)
	}
	clock.fireAll()
	if tun.Running() != "osaka" {
		t.Errorf("after confirming running %q, want osaka", tun.Running())
	}
}

func TestConfirmWithNothingPendingSaysSo(t *testing.T) {
	d, _, _ := newDaemon(t)
	if err := d.Confirm(); err == nil || !strings.Contains(err.Error(), "nothing to confirm") {
		t.Fatalf("err = %v, want nothing to confirm", err)
	}
}

// Going down is a change like any other: an agent that downs the tunnel
// by mistake gets it back.
func TestUnconfirmedDownComesBackUp(t *testing.T) {
	d, tun, clock := newDaemon(t)
	mustApply(t, d, daemon.Change{Spec: ptr(spec("tokyo"))})
	mustApply(t, d, daemon.Change{Spec: nil, ConfirmWithin: 10 * time.Second})
	if tun.Running() != "" {
		t.Fatalf("running %q right after down, want nothing", tun.Running())
	}
	clock.fireAll()
	if tun.Running() != "tokyo" {
		t.Errorf("after the deadline running %q, want tokyo back", tun.Running())
	}
}

func TestFailedStartRestoresThePreviousNode(t *testing.T) {
	d, tun, _ := newDaemon(t)
	tun.failFor = "broken"
	mustApply(t, d, daemon.Change{Spec: ptr(spec("tokyo"))})
	err := d.Apply(context.Background(), daemon.Change{Spec: ptr(spec("broken"))})
	if err == nil || !strings.Contains(err.Error(), "utun refused") || !strings.Contains(err.Error(), "tokyo") {
		t.Fatalf("err = %v, want the start error and that tokyo was restored", err)
	}
	if tun.Running() != "tokyo" {
		t.Errorf("running %q, want tokyo restored", tun.Running())
	}
}

func TestFailedProbeRollsBackAtOnce(t *testing.T) {
	tun := &fakeTunnel{}
	clock := &fakeClock{}
	d := daemon.New(tun, clock, probeFunc(func(_ context.Context, url string) error {
		if tun.Running() == "osaka" {
			return errors.New("timeout reaching " + url)
		}
		return nil
	}))
	mustApply(t, d, daemon.Change{Spec: ptr(spec("tokyo"))})
	err := d.Apply(context.Background(), daemon.Change{Spec: ptr(spec("osaka")), Probe: "https://api.anthropic.com"})
	if err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("err = %v, want a rolled-back error", err)
	}
	if tun.Running() != "tokyo" {
		t.Errorf("running %q, want tokyo", tun.Running())
	}
}

func TestChangeWhilePendingIsRefused(t *testing.T) {
	d, _, _ := newDaemon(t)
	mustApply(t, d, daemon.Change{Spec: ptr(spec("osaka")), ConfirmWithin: 30 * time.Second})
	err := d.Apply(context.Background(), daemon.Change{Spec: ptr(spec("kyoto"))})
	if err == nil || !strings.Contains(err.Error(), "celare confirm") {
		t.Fatalf("err = %v, want a hint to confirm first", err)
	}
}

func mustApply(t *testing.T, d *daemon.Daemon, c daemon.Change) {
	t.Helper()
	if err := d.Apply(context.Background(), c); err != nil {
		t.Fatal(err)
	}
}

func ptr[T any](v T) *T { return &v }
