// Package daemon owns the tunnel's lifecycle. Every change can be made
// provisional: unless it is confirmed in time, or if a probe through the
// new state fails, the daemon puts the previous state back by itself.
// That is what lets an agent switch nodes over the very connection it is
// using without stranding itself offline.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Mergemat/celare/internal/config"
	"github.com/Mergemat/celare/internal/node"
)

// Spec is a fully resolved tunnel: the node and the routing that was in
// config.toml when the change was made. Rolling back restores a Spec as
// it was, even if config.toml has been edited since.
type Spec struct {
	NodeName string        `json:"node"`
	Node     node.Node     `json:"-"`
	Default  config.Action `json:"default"`
	Rules    []config.Rule `json:"rules"`
	DNS      []string      `json:"dns"`
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

// Change moves the tunnel to Spec, or down when Spec is nil.
type Change struct {
	Spec *Spec
	// ConfirmWithin, when set, reverts the change unless Confirm is
	// called before it elapses.
	ConfirmWithin time.Duration
	// Probe, when set, is fetched through the new state; failure reverts
	// the change immediately.
	Probe string
}

type Pending struct {
	Deadline  time.Time `json:"deadline"`
	RevertsTo string    `json:"revertsTo"`
}

type Event struct {
	Time    time.Time `json:"time"`
	Message string    `json:"message"`
}

type Status struct {
	State     string    `json:"state"` // "up" or "down"
	Node      string    `json:"node,omitempty"`
	Since     time.Time `json:"since"`
	Pending   *Pending  `json:"pending,omitempty"`
	LastEvent *Event    `json:"lastEvent,omitempty"`
	Events    []Event   `json:"events,omitempty"`
}

const maxEvents = 20

type Daemon struct {
	tunnel Tunnel
	clock  Clock
	prober Prober

	// op serializes state transitions; mu guards the fields below so
	// Status stays responsive while a transition (or probe) runs.
	op sync.Mutex
	mu sync.Mutex

	current *Spec
	since   time.Time
	pending *pendingChange
	events  []Event
}

type pendingChange struct {
	Pending
	previous *Spec
	stop     func() bool
}

func New(t Tunnel, c Clock, p Prober) *Daemon {
	return &Daemon{tunnel: t, clock: c, prober: p, since: c.Now()}
}

func describe(s *Spec) string {
	if s == nil {
		return "down"
	}
	return s.NodeName
}

// Apply performs a change. On failure the previous state is back in
// place when it returns.
func (d *Daemon) Apply(ctx context.Context, c Change) error {
	d.op.Lock()
	defer d.op.Unlock()

	d.mu.Lock()
	pending, previous := d.pending, d.current
	d.mu.Unlock()
	if pending != nil {
		return fmt.Errorf("a change is waiting for confirmation until %s; run `celare confirm` to keep it or `celare rollback` to undo it first",
			pending.Deadline.Format(time.RFC3339))
	}

	if err := d.switchTo(previous, c.Spec); err != nil {
		return err
	}
	if c.Probe != "" {
		pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := d.prober.Probe(pctx, c.Probe)
		cancel()
		if err != nil {
			restoreErr := d.switchTo(c.Spec, previous)
			msg := fmt.Sprintf("probe of %s through %s failed (%v); rolled back to %s", c.Probe, describe(c.Spec), err, describe(previous))
			d.record(msg)
			return errors.Join(errors.New(msg), restoreErr)
		}
	}
	if c.ConfirmWithin > 0 {
		p := &pendingChange{
			Pending:  Pending{Deadline: d.clock.Now().Add(c.ConfirmWithin), RevertsTo: describe(previous)},
			previous: previous,
		}
		p.stop = d.clock.AfterFunc(c.ConfirmWithin, func() { d.expire(p, c.ConfirmWithin) })
		d.mu.Lock()
		d.pending = p
		d.mu.Unlock()
		d.record(fmt.Sprintf("switched to %s; reverts to %s at %s unless confirmed", describe(c.Spec), describe(previous), p.Deadline.Format(time.RFC3339)))
		return nil
	}
	d.record("switched to " + describe(c.Spec))
	return nil
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
			startErr := fmt.Errorf("start %s: %w", describe(to), err)
			if from != nil {
				if err := d.tunnel.Start(*from); err != nil {
					d.setCurrent(nil)
					return fmt.Errorf("%w; restoring %s also failed: %v, so the tunnel is down", startErr, describe(from), err)
				}
			}
			d.record(fmt.Sprintf("%v; restored %s", startErr, describe(from)))
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

func (d *Daemon) expire(p *pendingChange, after time.Duration) {
	d.op.Lock()
	defer d.op.Unlock()
	d.mu.Lock()
	if d.pending != p {
		d.mu.Unlock()
		return
	}
	d.pending = nil
	current := d.current
	d.mu.Unlock()
	err := d.switchTo(current, p.previous)
	msg := fmt.Sprintf("change to %s was not confirmed within %s; rolled back to %s", describe(current), after, describe(p.previous))
	if err != nil {
		msg += fmt.Sprintf(" (with errors: %v)", err)
	}
	d.record(msg)
}

// Confirm keeps the pending change.
func (d *Daemon) Confirm() error {
	d.op.Lock()
	defer d.op.Unlock()
	d.mu.Lock()
	p := d.pending
	d.pending = nil
	current := d.current
	d.mu.Unlock()
	if p == nil {
		return errors.New("nothing to confirm: no change is pending")
	}
	p.stop()
	d.record("confirmed " + describe(current))
	return nil
}

// Rollback undoes the pending change now.
func (d *Daemon) Rollback() error {
	d.op.Lock()
	defer d.op.Unlock()
	d.mu.Lock()
	p := d.pending
	d.pending = nil
	current := d.current
	d.mu.Unlock()
	if p == nil {
		return errors.New("nothing to roll back: no change is pending")
	}
	p.stop()
	if err := d.switchTo(current, p.previous); err != nil {
		return err
	}
	d.record(fmt.Sprintf("rolled back to %s on request", describe(p.previous)))
	return nil
}

// Current returns the running spec, nil when down.
func (d *Daemon) Current() *Spec {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.current
}

func (d *Daemon) record(msg string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, Event{Time: d.clock.Now(), Message: msg})
	if len(d.events) > maxEvents {
		d.events = d.events[len(d.events)-maxEvents:]
	}
}

func (d *Daemon) Status() Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	st := Status{State: "down", Since: d.since, Events: append([]Event(nil), d.events...)}
	if d.current != nil {
		st.State, st.Node = "up", d.current.NodeName
	}
	if d.pending != nil {
		p := d.pending.Pending
		st.Pending = &p
	}
	if n := len(d.events); n > 0 {
		e := d.events[n-1]
		st.LastEvent = &e
	}
	return st
}
