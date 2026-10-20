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
	"github.com/Mergemat/celare/internal/node"
	"github.com/Mergemat/celare/internal/system"
	"github.com/google/go-cmp/cmp"
)

const netstatOut = `Internet:
Destination        Gateway            Flags               Netif Expire
default            192.168.1.1        UGScg                 en0
`

const serviceOrderOut = `(1) Wi-Fi
(Hardware Port: Wi-Fi, Device: en0)
`

// fakeRunner answers read-only commands from canned output and records
// the ones that change the system.
type fakeRunner struct {
	ncList   string
	netstat  string
	ran      [][]string
	failWhen string
}

func (r *fakeRunner) Run(name string, args ...string) (string, error) {
	cmd := append([]string{name}, args...)
	line := strings.Join(cmd, " ")
	switch {
	case line == "netstat -rn -f inet":
		return r.netstat, nil
	case line == "networksetup -listnetworkserviceorder":
		return serviceOrderOut, nil
	case line == "networksetup -getdnsservers Wi-Fi":
		return "1.1.1.1\n", nil
	case line == "ifconfig -l":
		return "lo0 en0 utun0 utun4\n", nil
	case line == "scutil --nc list":
		return r.ncList, nil
	}
	r.ran = append(r.ran, cmd)
	if r.failWhen != "" && strings.Contains(line, r.failWhen) {
		return "route: writing to routing socket: File exists", errors.New("exit status 1")
	}
	return "", nil
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
		Run:         r.Run,
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
	want := [][]string{
		{"route", "-n", "add", "-ifscope", "en0", "default", "192.168.1.1"},
		{"route", "-n", "add", "-net", "0.0.0.0/1", "-interface", "utun5"},
		{"route", "-n", "add", "-net", "128.0.0.0/1", "-interface", "utun5"},
		{"route", "-n", "add", "-inet6", "-net", "::/1", "-interface", "utun5"},
		{"route", "-n", "add", "-inet6", "-net", "8000::/1", "-interface", "utun5"},
		{"networksetup", "-setdnsservers", "Wi-Fi", "169.254.10.1"},
	}
	if diff := cmp.Diff(want, r.ran); diff != "" {
		t.Errorf("commands mismatch (-want +got):\n%s", diff)
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
	want := [][]string{
		{"route", "-n", "delete", "-net", "0.0.0.0/1", "-interface", "utun5"},
		{"route", "-n", "delete", "-net", "128.0.0.0/1", "-interface", "utun5"},
		{"route", "-n", "delete", "-inet6", "-net", "::/1", "-interface", "utun5"},
		{"route", "-n", "delete", "-inet6", "-net", "8000::/1", "-interface", "utun5"},
		{"route", "-n", "delete", "-ifscope", "en0", "default", "192.168.1.1"},
		{"networksetup", "-setdnsservers", "Wi-Fi", "1.1.1.1"},
	}
	if diff := cmp.Diff(want, r.ran); diff != "" {
		t.Errorf("commands mismatch (-want +got):\n%s", diff)
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
	last := r.ran[len(r.ran)-1]
	if diff := cmp.Diff([]string{"networksetup", "-setdnsservers", "Wi-Fi", "1.1.1.1"}, last); diff != "" {
		t.Errorf("last command should restore DNS (-want +got):\n%s", diff)
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
	fresh := &system.Tunnel{Run: r.Run, RestorePath: restore}
	if err := fresh.Recover(); err != nil {
		t.Fatal(err)
	}
	if len(r.ran) != 6 || r.ran[5][len(r.ran[5])-1] != "1.1.1.1" {
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
	if err == nil || !strings.Contains(err.Error(), "no physical network") {
		t.Fatalf("err = %v, want no physical network", err)
	}
}

// With V2Box connected, celare goes out through it like any other app:
// no refusal, the server connection is bound to V2Box's tunnel, and DNS is
// still saved and restored on the Wi-Fi service.
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
	if last := r.ran[len(r.ran)-1]; strings.Join(last, " ") != "networksetup -setdnsservers Wi-Fi 169.254.10.1" {
		t.Errorf("last command %v, want DNS set on Wi-Fi", last)
	}
	if tun.Interface() != "utun4" {
		t.Errorf("Interface() = %q, want utun4", tun.Interface())
	}
	// Without a default scoped to utun4, Xray's bound connections would
	// follow the tunnel's own routes back into it.
	if first := strings.Join(r.ran[0], " "); first != "route -n add -ifscope utun4 default -interface utun4" {
		t.Errorf("first command %q, want a default route scoped to utun4", first)
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
		if slices.Contains(cmd, "-ifscope") {
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
	if last := strings.Join(r.ran[len(r.ran)-1], " "); last != "networksetup -setdnsservers Wi-Fi 1.1.1.1" {
		t.Errorf("last command %q, want DNS restored", last)
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
