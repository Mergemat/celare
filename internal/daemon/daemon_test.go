package daemon_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mergemat/celare/internal/config"
	"github.com/Mergemat/celare/internal/daemon"
	"github.com/Mergemat/celare/internal/node"
	"github.com/google/go-cmp/cmp"
)

// fakeTunnel records what is running instead of touching the network.
type fakeTunnel struct {
	mu      sync.Mutex
	running string // node name, "" when down
	rules   int
	failFor string // Start fails for this node
}

func (f *fakeTunnel) Start(s daemon.Spec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s.NodeName == f.failFor {
		return errors.New("utun refused")
	}
	f.running, f.rules = s.NodeName, len(s.Rules)
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

func spec(name string, rules ...config.Rule) *daemon.Spec {
	return &daemon.Spec{NodeName: name, Node: node.Node{Name: name}, Default: config.Proxy, Rules: rules}
}

func newDaemon(t *testing.T) (*daemon.Daemon, *fakeTunnel, *fakeClock) {
	t.Helper()
	tun := &fakeTunnel{}
	clock := &fakeClock{now: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}
	d := daemon.New(tun, clock, probeFunc(func(context.Context, string) error { return nil }))
	return d, tun, clock
}

func connect(t *testing.T, d *daemon.Daemon, r daemon.Request) daemon.Change {
	t.Helper()
	c, err := d.Apply(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestConnectWithoutConfirmIsCommitted(t *testing.T) {
	d, tun, clock := newDaemon(t)
	c := connect(t, d, daemon.Request{Action: daemon.Connect, Spec: spec("tokyo")})
	clock.fireAll()
	if c.State != daemon.Committed || c.ID == "" {
		t.Errorf("change = %+v, want committed with an id", c)
	}
	if tun.Running() != "tokyo" {
		t.Errorf("running %q, want tokyo", tun.Running())
	}
	if st := d.Tunnel(); st.State != "up" || st.Node != "tokyo" || st.Pending != nil {
		t.Errorf("tunnel = %+v, want up on tokyo with nothing pending", st)
	}
}

// The agent switches nodes, loses its connection, and never confirms:
// the daemon must put the old node back on its own.
func TestUnconfirmedChangeReverts(t *testing.T) {
	d, tun, clock := newDaemon(t)
	connect(t, d, daemon.Request{Action: daemon.Connect, Spec: spec("tokyo")})
	c := connect(t, d, daemon.Request{Action: daemon.Connect, Spec: spec("osaka"), ConfirmWithin: 30 * time.Second})

	if c.State != daemon.Pending || c.Deadline == nil || !c.Deadline.Equal(clock.now.Add(30*time.Second)) {
		t.Fatalf("change = %+v, want pending with a 30s deadline", c)
	}
	if c.From == nil || c.From.Node != "tokyo" || c.To == nil || c.To.Node != "osaka" {
		t.Errorf("change from %+v to %+v, want tokyo to osaka", c.From, c.To)
	}
	clock.fireAll()
	if tun.Running() != "tokyo" {
		t.Errorf("after the deadline running %q, want tokyo back", tun.Running())
	}
	got, ok := d.Change(c.ID)
	if !ok || got.State != daemon.Reverted || !strings.Contains(got.Reason, "not confirmed") {
		t.Errorf("change after deadline = %+v, want reverted because it was not confirmed", got)
	}
	if st := d.Tunnel(); st.Pending != nil || st.Node != "tokyo" {
		t.Errorf("tunnel after revert = %+v, want tokyo with nothing pending", st)
	}
}

func TestConfirmByIDKeepsTheChange(t *testing.T) {
	d, tun, clock := newDaemon(t)
	c := connect(t, d, daemon.Request{Action: daemon.Connect, Spec: spec("osaka"), ConfirmWithin: 30 * time.Second})
	got, err := d.Confirm(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	clock.fireAll()
	if got.State != daemon.Committed || tun.Running() != "osaka" {
		t.Errorf("change %+v, running %q; want committed on osaka", got, tun.Running())
	}
}

// An agent confirming late must not confirm someone else's change.
func TestConfirmOfAnotherChangeIsRefused(t *testing.T) {
	d, _, clock := newDaemon(t)
	first := connect(t, d, daemon.Request{Action: daemon.Connect, Spec: spec("osaka"), ConfirmWithin: 30 * time.Second})
	clock.fireAll() // first reverts
	connect(t, d, daemon.Request{Action: daemon.Connect, Spec: spec("kyoto"), ConfirmWithin: 30 * time.Second})

	_, err := d.Confirm(first.ID)
	if !errors.Is(err, daemon.ErrNotPending) || !strings.Contains(err.Error(), "reverted") {
		t.Fatalf("err = %v, want ErrNotPending saying it was reverted", err)
	}
}

func TestConfirmWithEmptyIDTakesThePendingChange(t *testing.T) {
	d, _, _ := newDaemon(t)
	connect(t, d, daemon.Request{Action: daemon.Connect, Spec: spec("osaka"), ConfirmWithin: 30 * time.Second})
	if _, err := d.Confirm(""); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Confirm(""); !errors.Is(err, daemon.ErrNothingPending) {
		t.Fatalf("err = %v, want ErrNothingPending", err)
	}
}

func TestRevertNow(t *testing.T) {
	d, tun, _ := newDaemon(t)
	connect(t, d, daemon.Request{Action: daemon.Connect, Spec: spec("tokyo")})
	c := connect(t, d, daemon.Request{Action: daemon.Disconnect, ConfirmWithin: 30 * time.Second})
	got, err := d.Revert(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != daemon.Reverted || tun.Running() != "tokyo" {
		t.Errorf("change %+v, running %q; want reverted, tokyo back", got, tun.Running())
	}
}

func TestFailedStartIsAFailedChange(t *testing.T) {
	d, tun, _ := newDaemon(t)
	tun.failFor = "broken"
	connect(t, d, daemon.Request{Action: daemon.Connect, Spec: spec("tokyo")})
	c, err := d.Apply(context.Background(), daemon.Request{Action: daemon.Connect, Spec: spec("broken")})
	if !errors.Is(err, daemon.ErrStartFailed) || c.State != daemon.Failed || !strings.Contains(c.Reason, "utun refused") {
		t.Fatalf("change %+v, err %v; want failed with the start error", c, err)
	}
	if tun.Running() != "tokyo" {
		t.Errorf("running %q, want tokyo restored", tun.Running())
	}
}

func TestFailedProbeRevertsAtOnce(t *testing.T) {
	tun := &fakeTunnel{}
	d := daemon.New(tun, &fakeClock{}, probeFunc(func(_ context.Context, url string) error {
		if tun.Running() == "osaka" {
			return errors.New("timeout reaching " + url)
		}
		return nil
	}))
	connect(t, d, daemon.Request{Action: daemon.Connect, Spec: spec("tokyo")})
	c, err := d.Apply(context.Background(), daemon.Request{Action: daemon.Connect, Spec: spec("osaka"), Probe: "https://api.anthropic.com"})
	if !errors.Is(err, daemon.ErrProbeFailed) || c.State != daemon.Reverted {
		t.Fatalf("change %+v, err %v; want reverted by a failed probe", c, err)
	}
	if tun.Running() != "tokyo" {
		t.Errorf("running %q, want tokyo", tun.Running())
	}
}

func TestChangeWhilePendingIsRefused(t *testing.T) {
	d, _, _ := newDaemon(t)
	c := connect(t, d, daemon.Request{Action: daemon.Connect, Spec: spec("osaka"), ConfirmWithin: 30 * time.Second})
	_, err := d.Apply(context.Background(), daemon.Request{Action: daemon.Connect, Spec: spec("kyoto")})
	if !errors.Is(err, daemon.ErrPending) || !strings.Contains(err.Error(), c.ID) {
		t.Fatalf("err = %v, want ErrPending naming %s", err, c.ID)
	}
}

func TestDryRunPlansWithoutTouchingTheTunnel(t *testing.T) {
	d, tun, _ := newDaemon(t)
	lan := config.Rule{Name: "lan", Action: config.Direct, IPs: []string{"192.168.0.0/16"}}
	ads := config.Rule{Action: config.Block, Domains: []string{"geosite:category-ads-all"}}
	connect(t, d, daemon.Request{Action: daemon.Connect, Spec: spec("tokyo", lan)})
	c, err := d.Apply(context.Background(), daemon.Request{Action: daemon.Apply, Spec: spec("tokyo", ads), DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if c.State != daemon.Planned || tun.rules != 1 {
		t.Errorf("state %s with %d rules running, want planned and the old single rule", c.State, tun.rules)
	}
	want := daemon.Diff{Rules: &daemon.RulesDiff{Added: []config.Rule{ads}, Removed: []config.Rule{lan}}}
	if diff := cmp.Diff(want, c.Diff); diff != "" {
		t.Errorf("diff mismatch (-want +got):\n%s", diff)
	}
	if len(d.Changes()) != 1 {
		t.Errorf("a dry run was recorded in the history")
	}
}

func TestEventsAfterACursor(t *testing.T) {
	d, _, clock := newDaemon(t)
	connect(t, d, daemon.Request{Action: daemon.Connect, Spec: spec("tokyo")})
	cursor := d.Events(0)[len(d.Events(0))-1].Seq
	c := connect(t, d, daemon.Request{Action: daemon.Connect, Spec: spec("osaka"), ConfirmWithin: time.Second})
	clock.fireAll()
	var kinds []string
	for _, e := range d.Events(cursor) {
		if e.Change != c.ID {
			t.Errorf("event %+v is not about change %s", e, c.ID)
		}
		kinds = append(kinds, e.Kind)
	}
	if diff := cmp.Diff([]string{"change.pending", "change.reverted"}, kinds); diff != "" {
		t.Errorf("event kinds mismatch (-want +got):\n%s", diff)
	}
}
