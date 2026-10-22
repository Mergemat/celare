package cli

import (
	"context"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Mergemat/celare/internal/config"
	"github.com/Mergemat/celare/internal/daemon"
	"github.com/Mergemat/celare/internal/netconf"
	"github.com/Mergemat/celare/internal/node"
	"github.com/Mergemat/celare/internal/system"
)

// engines hands out engines that can die on their own.
type engines struct {
	mu      sync.Mutex
	started []*fakeEngine
}

type fakeEngine struct {
	done chan struct{}
	once sync.Once
}

func (e *fakeEngine) Close() error          { e.once.Do(func() { close(e.done) }); return nil }
func (e *fakeEngine) Done() <-chan struct{} { return e.done }

func (es *engines) start([]byte) (system.Engine, error) {
	es.mu.Lock()
	defer es.mu.Unlock()
	e := &fakeEngine{done: make(chan struct{})}
	es.started = append(es.started, e)
	return e, nil
}

func (es *engines) count() int {
	es.mu.Lock()
	defer es.mu.Unlock()
	return len(es.started)
}

func (es *engines) last() *fakeEngine {
	es.mu.Lock()
	defer es.mu.Unlock()
	return es.started[len(es.started)-1]
}

type noProbe struct{}

func (noProbe) Probe(context.Context, string) error { return nil }

// A crashed engine leaves the tunnel up on paper but carrying nothing;
// the service must bring it back without being asked, and must not
// mistake a disconnect for a crash.
func TestWatchTunnelRestartsAnEngineThatDies(t *testing.T) {
	es := &engines{}
	exits := make(chan struct{}, 1)
	tunnel := &system.Tunnel{
		StartEngine: es.start,
		Lookup:      func(string) ([]net.IP, error) { return []net.IP{net.ParseIP("203.0.113.9")}, nil },
		CanReach:    func(string, int, string) error { return nil },
		RestorePath: filepath.Join(t.TempDir(), "restore.json"),
		Routes: func() ([]netconf.Route, error) {
			return []netconf.Route{{Interface: "en0", Gateway: "192.168.1.1"}}, nil
		},
		Interfaces:  func() ([]string, error) { return []string{"lo0", "en0"}, nil },
		ChangeRoute: func(bool, system.RouteSpec) error { return nil },
		SetDNS:      func(bool) error { return nil },
		OnEngineExit: func() {
			select {
			case exits <- struct{}{}:
			default:
			}
		},
	}
	d := daemon.New(tunnel, wallClock{}, noProbe{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watchTunnel(ctx, d, tunnel, exits)

	tokyo := &daemon.Spec{NodeName: "tokyo", Default: config.Proxy, Node: node.Node{
		Protocol: "trojan", Address: "203.0.113.9", Port: 443, Password: "pw", Security: node.Security{Type: "tls"},
	}}
	if _, err := d.Apply(ctx, daemon.Request{Action: daemon.Connect, Spec: tokyo}); err != nil {
		t.Fatal(err)
	}
	es.last().Close() // dies without the tunnel stopping it
	deadline := time.Now().Add(5 * time.Second)
	for es.count() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if es.count() != 2 || tunnel.EngineExited() {
		t.Fatalf("%d engines started, exited = %v; want a second, running engine", es.count(), tunnel.EngineExited())
	}
	if st := d.Tunnel(); st.State != "up" || st.Node != "tokyo" {
		t.Errorf("tunnel = %+v, want up on tokyo", st)
	}

	if _, err := d.Apply(ctx, daemon.Request{Action: daemon.Disconnect}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if es.count() != 2 {
		t.Errorf("%d engines started after a disconnect, want no restart", es.count())
	}
}

func TestRestartDelayBacksOffAnEngineThatKeepsDying(t *testing.T) {
	var r restartDelay
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	var got []time.Duration
	for range 9 {
		wait := r.next(now)
		got = append(got, wait)
		now = now.Add(wait + time.Second) // dies a second after each restart
	}
	want := []time.Duration{0, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, time.Minute, time.Minute}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("waits = %v, want %v", got, want)
		}
	}
	// Running a while counts as recovered.
	if wait := r.next(now.Add(2 * time.Minute)); wait != 0 {
		t.Errorf("wait after two quiet minutes = %s, want 0", wait)
	}
}
