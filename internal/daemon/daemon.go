// Package daemon owns the tunnel's lifecycle. Every change to it is a
// Change with an id. A change can be provisional: unless it is confirmed
// in time, or if a probe through the new tunnel fails, the daemon puts
// the previous tunnel back by itself. That is what lets an agent switch
// nodes over the very connection it is using without stranding itself.
package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Mergemat/celare/internal/config"
	"github.com/Mergemat/celare/internal/node"
)

// Spec is a fully resolved tunnel: a node plus the policy from
// config.toml at the time. Reverting restores a Spec exactly, even if
// config.toml has been edited since.
type Spec struct {
	NodeName string
	Node     node.Node
	Default  config.Action
	Rules    []config.Rule
	DNS      []string
}

// Tunnel brings a Spec up on the system and takes it down.
type Tunnel interface {
	Start(Spec) error
	Stop() error
}

type Clock interface {
	Now() time.Time
	// AfterFunc runs f after d; the returned func cancels it.
	AfterFunc(d time.Duration, f func()) (stop func() bool)
}

// Prober checks that traffic flows through the current tunnel.
type Prober interface {
	Probe(ctx context.Context, url string) error
}

type Action string

const (
	Connect    Action = "connect"
	Disconnect Action = "disconnect"
	Apply      Action = "apply"
)

type State string

const (
	Planned   State = "planned" // a dry run; nothing changed
	Pending   State = "pending" // applied, waiting for confirmation
	Committed State = "committed"
	Reverted  State = "reverted"
	Failed    State = "failed" // never took effect
)

// Request asks for a change. Spec nil means down.
type Request struct {
	Action Action
	Spec   *Spec
	// ConfirmWithin, when set, reverts the change unless it is confirmed
	// before it elapses.
	ConfirmWithin time.Duration
	// Probe, when set, is fetched through the new tunnel; failure
	// reverts the change at once.
	Probe  string
	DryRun bool
}

// Endpoint is one side of a change; nil means the tunnel is down.
type Endpoint struct {
	Node string `json:"node"`
}

type Change struct {
	ID        string     `json:"id"`
	Action    Action     `json:"action"`
	State     State      `json:"state"`
	From      *Endpoint  `json:"from"`
	To        *Endpoint  `json:"to"`
	Diff      Diff       `json:"diff"`
	Probe     string     `json:"probe,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	Deadline  *time.Time `json:"deadline,omitempty"`
	SettledAt *time.Time `json:"settledAt,omitempty"`
	// Reason explains a reverted or failed change.
	Reason string `json:"reason,omitempty"`

	from, to *Spec
	stop     func() bool
}

// TunnelState is the running state.
type TunnelState struct {
	State   string    `json:"state"` // "up" or "down"
	Node    string    `json:"node,omitempty"`
	Since   time.Time `json:"since"`
	Pending *Change   `json:"pending,omitempty"`
}

// Event is something that happened, in order. Seq only grows.
type Event struct {
	Seq     int64     `json:"seq"`
	Time    time.Time `json:"time"`
	Kind    string    `json:"kind"` // change.committed, change.pending, change.reverted, change.failed, tunnel.restarted
	Change  string    `json:"change,omitempty"`
	Message string    `json:"message"`
}

// Errors callers can branch on.
var (
	ErrPending        = errors.New("a change is pending")
	ErrNothingPending = errors.New("no change is pending")
	ErrNotPending     = errors.New("change is not pending")
	ErrNotFound       = errors.New("no such change")
	ErrProbeFailed    = errors.New("traffic check failed")
	ErrStartFailed    = errors.New("tunnel failed to start")
)

const (
	keepChanges = 50
	keepEvents  = 200
	// probeTimeout bounds the traffic check; a working tunnel answers
	// in a second or two, even through another VPN.
	probeTimeout = 10 * time.Second
)

type Daemon struct {
	tunnel Tunnel
	clock  Clock
	prober Prober

	// OnSettled, if set, is called with the running spec whenever no
	// change is left pending, i.e. whenever the state is committed.
	OnSettled func(*Spec)

	// op serializes transitions; mu guards the fields below so reads stay
	// responsive while a transition or probe runs.
	op sync.Mutex
	mu sync.Mutex

	current *Spec
	since   time.Time
	pending *Change
	changes []*Change // oldest first
	events  []Event
	seq     int64
}

func New(t Tunnel, c Clock, p Prober) *Daemon {
	return &Daemon{tunnel: t, clock: c, prober: p, since: c.Now()}
}

func endpoint(s *Spec) *Endpoint {
	if s == nil {
		return nil
	}
	return &Endpoint{Node: s.NodeName}
}

func describe(s *Spec) string {
	if s == nil {
		return "down"
	}
	return s.NodeName
}

func newID() string {
	b := make([]byte, 3)
	rand.Read(b)
	return "c" + hex.EncodeToString(b)
}

// Apply performs (or with DryRun, plans) a change. Whatever happens, the
// tunnel is in a known state when it returns and the change says which.
func (d *Daemon) Apply(ctx context.Context, r Request) (Change, error) {
	d.op.Lock()
	defer d.op.Unlock()

	d.mu.Lock()
	pending, previous := d.pending, d.current
	d.mu.Unlock()

	c := &Change{
		ID:        newID(),
		Action:    r.Action,
		From:      endpoint(previous),
		To:        endpoint(r.Spec),
		Diff:      diffSpecs(previous, r.Spec),
		Probe:     r.Probe,
		CreatedAt: d.clock.Now(),
		from:      previous,
		to:        r.Spec,
	}
	if r.DryRun {
		c.State = Planned
		return *c, nil
	}
	if pending != nil {
		return Change{}, fmt.Errorf("%w: %s (%s) until %s; run `celare confirm %s` to keep it or `celare revert %s` to undo it first",
			ErrPending, pending.ID, pending.Action, pending.Deadline.Format(time.RFC3339), pending.ID, pending.ID)
	}

	if err := d.switchTo(previous, r.Spec); err != nil {
		c.State, c.Reason = Failed, err.Error()
		d.settle(c, "change.failed", fmt.Sprintf("%s %s failed: %v", c.Action, describe(r.Spec), err))
		return *c, err
	}
	if r.Probe != "" {
		// The check runs to the end even if the caller hangs up: giving up
		// early would revert a tunnel that works.
		pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), probeTimeout)
		err := d.prober.Probe(pctx, r.Probe)
		cancel()
		if err != nil {
			reason := fmt.Sprintf("nothing got through %s (%v, fetching %s)", describe(r.Spec), err, r.Probe)
			restoreErr := d.switchTo(r.Spec, previous)
			c.State, c.Reason = Reverted, "traffic check failed: "+reason
			d.settle(c, "change.reverted", fmt.Sprintf("%s reverted to %s: traffic check failed: %s", c.ID, describe(previous), reason))
			return *c, errors.Join(fmt.Errorf("%w: %s; reverted to %s", ErrProbeFailed, reason, describe(previous)), restoreErr)
		}
	}
	if r.ConfirmWithin > 0 {
		deadline := d.clock.Now().Add(r.ConfirmWithin)
		c.State, c.Deadline = Pending, &deadline
		c.stop = d.clock.AfterFunc(r.ConfirmWithin, func() { d.expire(c, r.ConfirmWithin) })
		d.mu.Lock()
		d.pending = c
		d.changes = appendCapped(d.changes, c)
		d.mu.Unlock()
		d.record("change.pending", c.ID, fmt.Sprintf("%s switched to %s; reverts to %s at %s unless confirmed",
			c.ID, describe(r.Spec), describe(previous), deadline.Format(time.RFC3339)))
		return *c, nil
	}
	c.State = Committed
	d.settle(c, "change.committed", fmt.Sprintf("%s switched to %s", c.ID, describe(r.Spec)))
	return *c, nil
}

// settle finishes a change that is not pending: stamps it, keeps it in
// the history, records the event and reports the committed state.
func (d *Daemon) settle(c *Change, kind, msg string) {
	now := d.clock.Now()
	c.SettledAt = &now
	d.mu.Lock()
	if d.pending == c {
		d.pending = nil // already in the history since it became pending
	} else {
		d.changes = appendCapped(d.changes, c)
	}
	current := d.current
	d.mu.Unlock()
	d.record(kind, c.ID, msg)
	if d.OnSettled != nil {
		d.OnSettled(current)
	}
}

func appendCapped(cs []*Change, c *Change) []*Change {
	cs = append(cs, c)
	if len(cs) > keepChanges {
		cs = cs[len(cs)-keepChanges:]
	}
	return cs
}

// switchTo replaces from with to on the system. If to fails to start,
// from is started again.
func (d *Daemon) switchTo(from, to *Spec) error {
	if from != nil {
		if err := d.tunnel.Stop(); err != nil {
			return fmt.Errorf("stop %s: %w", describe(from), err)
		}
	}
	if to != nil {
		if err := d.tunnel.Start(*to); err != nil {
			startErr := fmt.Errorf("%w: %s: %v", ErrStartFailed, describe(to), err)
			if from != nil {
				if err := d.tunnel.Start(*from); err != nil {
					d.setCurrent(nil)
					return fmt.Errorf("%w; restoring %s also failed (%v), so the tunnel is down", startErr, describe(from), err)
				}
			}
			return fmt.Errorf("%w; restored %s", startErr, describe(from))
		}
	}
	d.setCurrent(to)
	return nil
}

func (d *Daemon) setCurrent(s *Spec) {
	d.mu.Lock()
	d.current, d.since = s, d.clock.Now()
	d.mu.Unlock()
}

func (d *Daemon) expire(c *Change, after time.Duration) {
	d.op.Lock()
	defer d.op.Unlock()
	d.mu.Lock()
	still := d.pending == c
	d.mu.Unlock()
	if !still {
		return
	}
	err := d.switchTo(c.to, c.from)
	c.State = Reverted
	c.Reason = fmt.Sprintf("not confirmed within %s", after)
	if err != nil {
		c.Reason += fmt.Sprintf("; reverting hit errors: %v", err)
	}
	d.settle(c, "change.reverted", fmt.Sprintf("%s reverted to %s: %s", c.ID, describe(c.from), c.Reason))
}

// resolvePending finds the change id names (empty: the pending one) and
// checks it is the pending one.
func (d *Daemon) resolvePending(id string) (*Change, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if id == "" {
		if d.pending == nil {
			return nil, ErrNothingPending
		}
		return d.pending, nil
	}
	if d.pending != nil && d.pending.ID == id {
		return d.pending, nil
	}
	for _, c := range d.changes {
		if c.ID == id {
			return nil, fmt.Errorf("%w: %s is %s", ErrNotPending, id, c.State)
		}
	}
	return nil, fmt.Errorf("%w: %s; run `celare changes` for recent ids", ErrNotFound, id)
}

// Confirm keeps a pending change. id "" means whichever is pending.
func (d *Daemon) Confirm(id string) (Change, error) {
	d.op.Lock()
	defer d.op.Unlock()
	c, err := d.resolvePending(id)
	if err != nil {
		return Change{}, err
	}
	c.stop()
	c.State = Committed
	d.settle(c, "change.committed", fmt.Sprintf("%s confirmed; %s kept", c.ID, describe(c.to)))
	return *c, nil
}

// Revert undoes a pending change now. id "" means whichever is pending.
func (d *Daemon) Revert(id string) (Change, error) {
	d.op.Lock()
	defer d.op.Unlock()
	c, err := d.resolvePending(id)
	if err != nil {
		return Change{}, err
	}
	c.stop()
	err = d.switchTo(c.to, c.from)
	c.State, c.Reason = Reverted, "reverted on request"
	if err != nil {
		c.Reason += fmt.Sprintf("; reverting hit errors: %v", err)
	}
	d.settle(c, "change.reverted", fmt.Sprintf("%s reverted to %s on request", c.ID, describe(c.from)))
	return *c, err
}

// Restart stops and starts the running spec again, e.g. after the
// physical network changed. It does nothing while down or unless needed
// still holds: the caller looked at the tunnel before other changes
// were shut out, and may have seen one that is already gone.
func (d *Daemon) Restart(reason string, needed func() bool) error {
	d.op.Lock()
	defer d.op.Unlock()
	current := d.Current()
	if current == nil || !needed() {
		return nil
	}
	if err := d.switchTo(current, current); err != nil {
		d.record("tunnel.restarted", "", fmt.Sprintf("restarting %s (%s) failed: %v", describe(current), reason, err))
		return err
	}
	d.record("tunnel.restarted", "", fmt.Sprintf("restarted %s: %s", describe(current), reason))
	return nil
}

// Current returns the running spec, nil when down.
func (d *Daemon) Current() *Spec {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.current
}

func (d *Daemon) record(kind, change, msg string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seq++
	d.events = append(d.events, Event{Seq: d.seq, Time: d.clock.Now(), Kind: kind, Change: change, Message: msg})
	if len(d.events) > keepEvents {
		d.events = d.events[len(d.events)-keepEvents:]
	}
}

func (d *Daemon) Tunnel() TunnelState {
	d.mu.Lock()
	defer d.mu.Unlock()
	st := TunnelState{State: "down", Since: d.since}
	if d.current != nil {
		st.State, st.Node = "up", d.current.NodeName
	}
	if d.pending != nil {
		p := *d.pending
		st.Pending = &p
	}
	return st
}

// Change returns a change from the history.
func (d *Daemon) Change(id string) (Change, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, c := range d.changes {
		if c.ID == id {
			return *c, true
		}
	}
	return Change{}, false
}

// Changes returns recent changes, newest first.
func (d *Daemon) Changes() []Change {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Change, 0, len(d.changes))
	for i := len(d.changes) - 1; i >= 0; i-- {
		out = append(out, *d.changes[i])
	}
	return out
}

// Events returns the events with Seq greater than after, oldest first.
func (d *Daemon) Events(after int64) []Event {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []Event
	for _, e := range d.events {
		if e.Seq > after {
			out = append(out, e)
		}
	}
	return out
}
