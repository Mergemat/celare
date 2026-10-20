package system_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Mergemat/celare/internal/config"
	"github.com/Mergemat/celare/internal/daemon"
	"github.com/Mergemat/celare/internal/netconf"
	"github.com/Mergemat/celare/internal/node"
	"github.com/Mergemat/celare/internal/system"
)

const netstatOut = `Internet:
Destination        Gateway            Flags               Netif Expire
default            192.168.1.1        UGScg                 en0
`

const serviceOrderOut = `(1) Wi-Fi
(Hardware Port: Wi-Fi, Device: en0)
`

// fakeRunner records system effects, independent of their native encoding.
type fakeRunner struct {
	netstat  string
	ran      []string
	failWhen string
}

func (r *fakeRunner) has(effect string) bool { return slices.Contains(r.ran, effect) }

func (r *fakeRunner) route(add bool, spec system.RouteSpec) error {
	op := "delete"
	if add {
		op = "add"
	}
	line := fmt.Sprintf("%s %s via %s scoped=%v", op, spec.Prefix, spec.Interface, spec.Scoped)
	r.ran = append(r.ran, line)
	if add && r.failWhen != "" && strings.Contains(line, r.failWhen) {
		return errors.New("route: File exists")
	}
	return nil
}

func (r *fakeRunner) dns(enabled bool) error {
	r.ran = append(r.ran, fmt.Sprintf("dns %v", enabled))
	return nil
}

type fakeEngine struct {
	configs []string
	closed  int
}

func (e *fakeEngine) start(cfg []byte) (io.Closer, error) {
	e.configs = append(e.configs, string(cfg))
	return closer(func() error { e.closed++; return nil }), nil
}

type closer func() error

func (c closer) Close() error { return c() }

func newTunnel(t *testing.T, r *fakeRunner, e *fakeEngine) (*system.Tunnel, string) {
	t.Helper()
	state := filepath.Join(t.TempDir(), "restore.json")
	return &system.Tunnel{
		Routes:      func() ([]netconf.Route, error) { return netconf.ParseDefaultRoutes(r.netstat), nil },
		Interfaces:  func() ([]string, error) { return []string{"lo0", "en0", "utun0", "utun4"}, nil },
		ChangeRoute: r.route,
		SetDNS:      r.dns,
		StartEngine: e.start,
		CanReach:    func(string, int, string) error { return nil },
		Lookup: func(host string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("203.0.113.9")}, nil
		},
		RestorePath: state,
	}, state
}

var tokyo = daemon.Spec{
	NodeName: "tokyo",
	Node: node.Node{
		Protocol: "trojan", Address: "tokyo.example.test", Port: 443, Password: "pw",
		Security: node.Security{Type: "tls"},
	},
	Default: config.Proxy,
}

func TestStartRoutesIntoAFreshUtunAndBindsToWiFi(t *testing.T) {
	r := &fakeRunner{netstat: netstatOut}
	e := &fakeEngine{}
	tun, restore := newTunnel(t, r, e)
	if err := tun.Start(tokyo); err != nil {
		t.Fatal(err)
	}
	if !r.has("add 0.0.0.0/0 via en0 scoped=true") || !r.has("add 0.0.0.0/1 via utun5 scoped=false") || !r.has("add ::/1 via utun5 scoped=false") || !r.has("dns true") {
		t.Fatalf("missing tunnel setup: %v", r.ran)
	}
	if r.ran[0] != "add 0.0.0.0/0 via en0 scoped=true" || r.ran[len(r.ran)-1] != "dns true" {
		t.Fatalf("upstream must be pinned first and DNS changed last: %v", r.ran)
	}

	var cfg struct {
		Inbounds []struct {
			Settings struct{ Name string } `json:"settings"`
		} `json:"inbounds"`
		Outbounds []struct {
			Tag      string `json:"tag"`
			Settings struct {
				Servers []struct{ Address string } `json:"servers"`
			} `json:"settings"`
			StreamSettings struct {
				Sockopt     struct{ Interface string } `json:"sockopt"`
				TLSSettings struct {
					ServerName string `json:"serverName"`
				} `json:"tlsSettings"`
			} `json:"streamSettings"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(e.configs[0]), &cfg); err != nil {
		t.Fatal(err)
	}
	proxy := cfg.Outbounds[0]
	if cfg.Inbounds[0].Settings.Name != "utun5" {
		t.Errorf("tun inbound name %q, want utun5", cfg.Inbounds[0].Settings.Name)
	}
	if proxy.StreamSettings.Sockopt.Interface != "en0" {
		t.Errorf("proxy bound to %q, want en0", proxy.StreamSettings.Sockopt.Interface)
	}
	// The server's domain is resolved before DNS moves into the tunnel;
	// otherwise Xray would need the tunnel to find the tunnel. The domain
	// stays the TLS server name.
	if got := proxy.Settings.Servers[0].Address; got != "203.0.113.9" {
		t.Errorf("proxy address %q, want the pre-resolved 203.0.113.9", got)
	}
	if got := proxy.StreamSettings.TLSSettings.ServerName; got != "tokyo.example.test" {
		t.Errorf("TLS server name %q, want tokyo.example.test", got)
	}
	if _, err := os.Stat(restore); err != nil {
		t.Errorf("no restore file while up: %v", err)
	}
}

func TestStopRestoresDNSAndRemovesRoutes(t *testing.T) {
	r := &fakeRunner{netstat: netstatOut}
	e := &fakeEngine{}
	tun, restore := newTunnel(t, r, e)
	if err := tun.Start(tokyo); err != nil {
		t.Fatal(err)
	}
	r.ran = nil
	if err := tun.Stop(); err != nil {
		t.Fatal(err)
	}
	if r.ran[0] != "dns false" || !r.has("delete 0.0.0.0/1 via utun5 scoped=false") || !r.has("delete 8000::/1 via utun5 scoped=false") || !r.has("delete 0.0.0.0/0 via en0 scoped=true") {
		t.Fatalf("DNS must be restored before routes are removed: %v", r.ran)
	}

	if e.closed != 1 {
		t.Errorf("engine closed %d times, want 1", e.closed)
	}
	if _, err := os.Stat(restore); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("restore file still present after stop: %v", err)
	}
}

func TestFailedRouteUndoesEverything(t *testing.T) {
	r := &fakeRunner{netstat: netstatOut, failWhen: "128.0.0.0/1"}
	e := &fakeEngine{}
	tun, restore := newTunnel(t, r, e)
	err := tun.Start(tokyo)
	if err == nil || !strings.Contains(err.Error(), "File exists") {
		t.Fatalf("err = %v, want the route error", err)
	}
	if !r.has("dns false") || !r.has("delete 0.0.0.0/1 via utun5 scoped=false") {
		t.Errorf("ran %v, want routes removed and DNS restored", r.ran)
	}
	if e.closed != 1 {
		t.Errorf("engine closed %d times, want 1", e.closed)
	}
	if _, err := os.Stat(restore); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("restore file left behind: %v", err)
	}
}

// After a crash the daemon restarts and finds the restore file: the
// system must get its routes and DNS back before anything else.
func TestRecoverUndoesACrashedSession(t *testing.T) {
	r := &fakeRunner{netstat: netstatOut}
	e := &fakeEngine{}
	crashed, restore := newTunnel(t, r, e)
	if err := crashed.Start(tokyo); err != nil {
		t.Fatal(err)
	}
	r.ran = nil
	fresh := &system.Tunnel{ChangeRoute: r.route, SetDNS: r.dns, RestorePath: restore}
	if err := fresh.Recover(); err != nil {
		t.Fatal(err)
	}
	if len(r.ran) != 6 || !r.has("dns false") {
		t.Errorf("recover ran %v, want the five route deletes and the DNS restore", r.ran)
	}
	if _, err := os.Stat(restore); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("restore file still present after recover: %v", err)
	}
}

func TestStartWithoutNetworkExplainsWhy(t *testing.T) {
	r := &fakeRunner{netstat: "Internet:\n"}
	tun, _ := newTunnel(t, r, &fakeEngine{})
	err := tun.Start(tokyo)
	if err == nil || !strings.Contains(err.Error(), "connect Wi-Fi or Ethernet") {
		t.Fatalf("err = %v, want it to ask for Wi-Fi or Ethernet", err)
	}
}

// With V2Box connected, celare goes out through it like any other app:
// no refusal, the server connection is bound to V2Box's tunnel, and DNS is
// overridden only for this session.
func TestStartGoesOutThroughAnotherVPN(t *testing.T) {
	r := &fakeRunner{netstat: "Internet:\ndefault  link#29  UCSg  utun4\ndefault  192.168.1.1  UGScIg  en0\n"}
	e := &fakeEngine{}
	tun, _ := newTunnel(t, r, e)
	var reachedVia string
	tun.CanReach = func(_ string, _ int, iface string) error { reachedVia = iface; return nil }
	if err := tun.Start(tokyo); err != nil {
		t.Fatal(err)
	}
	if reachedVia != "utun4" {
		t.Errorf("checked the server via %q, want utun4", reachedVia)
	}
	if !strings.Contains(e.configs[0], `"interface": "utun4"`) {
		t.Errorf("outbounds not bound to utun4:\n%s", e.configs[0])
	}
	if !r.has("dns true") {
		t.Errorf("ran %v, want the temporary DNS override", r.ran)
	}
	if tun.Interface() != "utun4" {
		t.Errorf("Interface() = %q, want utun4", tun.Interface())
	}
	// Without a default scoped to utun4, Xray's bound connections would
	// follow the tunnel's own routes back into it.
	if !r.has("add 0.0.0.0/0 via utun4 scoped=true") {
		t.Errorf("ran %v, want a default route scoped to utun4", r.ran)
	}
}

// macOS's own scoped default is used as it is and left in place on stop.
func TestStartKeepsAnExistingScopedDefault(t *testing.T) {
	r := &fakeRunner{netstat: netstatOut + "default            192.168.1.1        UGScIg                en0\n"}
	tun, _ := newTunnel(t, r, &fakeEngine{})
	if err := tun.Start(tokyo); err != nil {
		t.Fatal(err)
	}
	if err := tun.Stop(); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range r.ran {
		if strings.Contains(cmd, "scoped=true") {
			t.Errorf("touched the system's scoped default: %v", cmd)
		}
	}
}

// A server that only fails once the routes are in must not leave the
// machine routed into a dead tunnel.
func TestStartUndoesWhenTheServerStopsAnsweringOnceRouted(t *testing.T) {
	r := &fakeRunner{netstat: netstatOut}
	e := &fakeEngine{}
	tun, restore := newTunnel(t, r, e)
	checks := 0
	tun.CanReach = func(string, int, string) error {
		if checks++; checks > 1 {
			return errors.New("i/o timeout")
		}
		return nil
	}
	err := tun.Start(tokyo)
	if err == nil || !strings.Contains(err.Error(), "once traffic was routed into the tunnel") {
		t.Fatalf("err = %v, want the post-route reach failure", err)
	}
	if !r.has("dns false") {
		t.Errorf("ran %v, want DNS restored", r.ran)
	}
	if e.closed != 1 || tun.Interface() != "" {
		t.Errorf("engine closed %d times, interface %q; want it stopped", e.closed, tun.Interface())
	}
	if _, err := os.Stat(restore); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("restore file left behind: %v", err)
	}
}

// An unreachable server must be found before touching routes and DNS,
// and the error must keep the cause.
func TestStartFailsFastWhenTheServerIsUnreachable(t *testing.T) {
	r := &fakeRunner{netstat: netstatOut}
	e := &fakeEngine{}
	tun, _ := newTunnel(t, r, e)
	var asked string
	tun.CanReach = func(host string, port int, iface string) error {
		asked = fmt.Sprintf("%s:%d via %s", host, port, iface)
		return errors.New("connect: connection refused")
	}
	err := tun.Start(tokyo)
	if err == nil || !strings.Contains(err.Error(), "can't reach the server") || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("err = %v, want it to say the server is unreachable and keep the cause", err)
	}
	if asked != "203.0.113.9:443 via en0" {
		t.Errorf("checked %q, want the resolved server via en0", asked)
	}
	if len(r.ran) != 0 || len(e.configs) != 0 {
		t.Errorf("changed the system anyway: ran %v, started %d engines", r.ran, len(e.configs))
	}
}

func TestFailedDNSCleanupKeepsRestoreFileForRetry(t *testing.T) {
	r := &fakeRunner{netstat: netstatOut}
	e := &fakeEngine{}
	tun, restore := newTunnel(t, r, e)
	if err := tun.Start(tokyo); err != nil {
		t.Fatal(err)
	}
	tun.SetDNS = func(bool) error { return errors.New("configd unavailable") }
	if err := tun.Stop(); err == nil || !strings.Contains(err.Error(), "configd unavailable") {
		t.Fatalf("lost DNS error: %v", err)
	}
	if e.closed != 1 {
		t.Fatal("cleanup left engine running")
	}
	if _, err := os.Stat(restore); err != nil {
		t.Fatalf("lost recovery data: %v", err)
	}
	tun.SetDNS = r.dns
	if err := tun.Recover(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(restore); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("restore file remains: %v", err)
	}
}
