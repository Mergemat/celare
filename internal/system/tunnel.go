// Package system brings a tunnel up on macOS: Xray's TUN inbound, the
// routes that send traffic into it, and DNS pointed at it. Everything it
// changes is written to a restore file first, so a crashed session can
// be undone on the next start.
package system

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Mergemat/celare/internal/config"
	"github.com/Mergemat/celare/internal/daemon"
	"github.com/Mergemat/celare/internal/netconf"
	"github.com/Mergemat/celare/internal/node"
	"github.com/Mergemat/celare/internal/xrayconf"
)

// Engine is a running Xray.
type Engine interface {
	io.Closer
	// Done is closed once the engine has exited, closed or not.
	Done() <-chan struct{}
}

// Tunnel implements daemon.Tunnel.
type Tunnel struct {
	// StartEngine starts Xray with a JSON config.
	StartEngine func(configJSON []byte) (Engine, error)
	// Lookup resolves the server's domain before DNS moves into the tunnel.
	Lookup func(host string) ([]net.IP, error)
	// XrayLog, if set, is where the tunnel's Xray logs at info level,
	// truncated at each start.
	XrayLog string
	// CanReach opens a TCP connection to host:port bound to iface, the way
	// Xray will reach the server once the tunnel is up.
	CanReach    func(host string, port int, iface string) error
	RestorePath string
	// Native system operations, replaceable in tests.
	Routes      func() ([]netconf.Route, error)
	Interfaces  func() ([]string, error)
	ChangeRoute func(add bool, route RouteSpec) error
	SetDNS      func(enabled bool) error
	// Logf, if set, gets a line per start and stop saying where the time
	// went.
	Logf func(format string, args ...any)
	// OnEngineExit, if set, is called when the running tunnel's engine
	// exits on its own, leaving routes and DNS pointing at a dead utun.
	OnEngineExit func()

	// mu guards session: status reads it while the daemon changes it.
	mu      sync.Mutex
	session *session
}

// session is what Stop needs to undo Start; it is also the restore file.
type session struct {
	Tun         string     `json:"tun"`
	Interface   string     `json:"interface"`
	ScopedRoute *RouteSpec `json:"scopedRoute,omitempty"`
	engine      Engine
}

// Exec runs a command for real.
func Exec(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}

func (t *Tunnel) routes() ([]netconf.Route, error) {
	if t.Routes != nil {
		return t.Routes()
	}
	return DefaultRoutes()
}

// Physical reports the physical default route and its network service.
func (t *Tunnel) Physical() (route netconf.Route, service string, err error) {
	routes, err := t.routes()
	if err != nil {
		return netconf.Route{}, "", err
	}
	return t.physical(routes)
}

func (t *Tunnel) physical(routes []netconf.Route) (route netconf.Route, service string, err error) {
	route, ok := netconf.PhysicalDefault(routes)
	if !ok {
		return netconf.Route{}, "", errors.New("no physical network: there is no default route through real hardware; connect Wi-Fi or Ethernet first")
	}
	order, err := Exec("networksetup", "-listnetworkserviceorder")
	if err != nil {
		return netconf.Route{}, "", fmt.Errorf("list network services: %w", err)
	}
	service, ok = netconf.ServiceForDevice(netconf.ParseServiceOrder(order), route.Interface)
	if !ok {
		return netconf.Route{}, "", fmt.Errorf("no enabled network service uses %s", route.Interface)
	}
	return route, service, nil
}

// Upstream is the route celare's own traffic leaves by: the system's
// primary default route other than celare's tunnel ours ("" when down).
func (t *Tunnel) Upstream(ours string) (netconf.Route, error) {
	routes, err := t.routes()
	if err != nil {
		return netconf.Route{}, err
	}
	return upstream(routes, ours)
}

func upstream(routes []netconf.Route, ours string) (netconf.Route, error) {
	up, ok := netconf.Upstream(routes, ours)
	if !ok {
		return netconf.Route{}, errors.New("no network: there is no default route; connect Wi-Fi or Ethernet first")
	}
	return up, nil
}

// Interface is the upstream interface the running tunnel is bound to.
func (t *Tunnel) Interface() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.session == nil {
		return ""
	}
	return t.session.Interface
}

// EngineExited reports whether the running tunnel's engine has exited.
func (t *Tunnel) EngineExited() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.session == nil {
		return false
	}
	select {
	case <-t.session.engine.Done():
		return true
	default:
		return false
	}
}

// watchEngine reports the session's engine exiting while the session
// runs. Stop ends the session before closing the engine.
func (t *Tunnel) watchEngine(s *session) {
	<-s.engine.Done()
	t.mu.Lock()
	running := t.session == s
	t.mu.Unlock()
	if running {
		t.logf("the engine of %s exited on its own", s.Tun)
		if t.OnEngineExit != nil {
			t.OnEngineExit()
		}
	}
}

// TunName is the utun interface of the running tunnel.
func (t *Tunnel) TunName() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.session == nil {
		return ""
	}
	return t.session.Tun
}

func (t *Tunnel) Start(s daemon.Spec) error {
	t.mu.Lock()
	running := t.session != nil
	t.mu.Unlock()
	if running {
		return errors.New("tunnel already running")
	}
	clock := newStopwatch()
	routes, err := t.routes()
	if err != nil {
		return err
	}
	up, err := upstream(routes, "")
	if err != nil {
		return err
	}
	clock.lap("network")
	n, err := t.pinAddress(s.Node)
	if err != nil {
		return err
	}
	clock.lap("resolve")
	if err := t.reach(n, up.Interface); err != nil {
		return fmt.Errorf("can't reach the server %s:%d over %s: %v", n.Address, n.Port, up.Interface, err)
	}
	clock.lap("reach")
	ifaces, err := t.interfaceNames()
	if err != nil {
		return fmt.Errorf("list interfaces: %w", err)
	}
	sess := &session{
		Tun:       netconf.FreeTunName(ifaces),
		Interface: up.Interface,
	}
	if !netconf.HasScopedDefault(routes, up.Interface) {
		sess.ScopedRoute = &RouteSpec{Prefix: netip.MustParsePrefix("0.0.0.0/0"), Interface: up.Interface, Scoped: true}
		if net.ParseIP(up.Gateway) != nil {
			sess.ScopedRoute.Gateway = up.Gateway
		}
	}
	clock.lap("checks")

	dns := s.DNS
	if len(dns) == 0 {
		dns = config.DefaultDNS
	}
	cfg, err := xrayconf.Config(xrayconf.Plan{
		Node:          n,
		Default:       s.Default,
		Rules:         s.Rules,
		DNS:           dns,
		TUN:           &xrayconf.TUN{Name: sess.Tun, MTU: 1500},
		BindInterface: up.Interface,
		LogLevel:      logLevel(t.XrayLog),
		LogFile:       t.XrayLog,
	})
	if err != nil {
		return err
	}
	if t.XrayLog != "" {
		os.WriteFile(t.XrayLog, nil, 0o644)
	}
	if sess.engine, err = t.StartEngine(cfg); err != nil {
		return err
	}
	if err := t.writeRestore(sess); err != nil {
		sess.engine.Close()
		return fmt.Errorf("write restore file: %w", err)
	}
	clock.lap("engine")

	// Establish the bound upstream before installing the tunnel routes.
	// DNS changes last so queries cannot enter an unrouted tunnel.
	if err := t.setRoutes(true, sess); err != nil {
		t.undo(sess)
		return fmt.Errorf("%w; changes undone", err)
	}
	clock.lap("routes")
	if err := t.setDNS(true); err != nil {
		t.undo(sess)
		return fmt.Errorf("%w; changes undone", err)
	}
	clock.lap("dns")
	// With the tunnel's routes in place, the server must still be
	// reachable the way Xray dials it, or every connection would fail.
	if err := t.reach(n, up.Interface); err != nil {
		t.undo(sess)
		return fmt.Errorf("the server %s:%d stopped answering over %s once traffic was routed into the tunnel: %v; changes undone", n.Address, n.Port, up.Interface, err)
	}
	clock.lap("recheck")
	t.logf("started %s via %s: %s", sess.Tun, up.Interface, clock)
	t.mu.Lock()
	t.session = sess
	t.mu.Unlock()
	go t.watchEngine(sess)
	return nil
}

// reach checks the server the way Xray dials it, where that can be
// checked (TCP).
func (t *Tunnel) reach(n node.Node, iface string) error {
	if !usesTCP(n) || t.CanReach == nil {
		return nil
	}
	return t.CanReach(n.Address, n.Port, iface)
}

func (t *Tunnel) interfaceNames() ([]string, error) {
	if t.Interfaces != nil {
		return t.Interfaces()
	}
	return InterfaceNames()
}

func (t *Tunnel) setDNS(enabled bool) error {
	if t.SetDNS != nil {
		return t.SetDNS(enabled)
	}
	return SetTunnelDNS(enabled)
}

func (t *Tunnel) changeRoute(add bool, spec RouteSpec) error {
	change := t.ChangeRoute
	if change == nil {
		change = ChangeRoute
	}
	if err := change(add, spec); err != nil {
		return fmt.Errorf("route %s via %s: %w", spec.Prefix, spec.Interface, err)
	}
	return nil
}

func (t *Tunnel) setRoutes(add bool, s *session) error {
	if add && s.ScopedRoute != nil {
		if err := t.changeRoute(true, *s.ScopedRoute); err != nil {
			return err
		}
	}
	var errs []error
	for _, spec := range tunnelRoutes(s.Tun) {
		errs = append(errs, t.changeRoute(add, spec))
	}
	if !add && s.ScopedRoute != nil {
		errs = append(errs, t.changeRoute(false, *s.ScopedRoute))
	}
	return errors.Join(errs...)
}

func (t *Tunnel) logf(format string, args ...any) {
	if t.Logf != nil {
		t.Logf(format, args...)
	}
}

// stopwatch names the stages of a start and how long each took.
type stopwatch struct {
	last   time.Time
	stages []string
}

func newStopwatch() *stopwatch { return &stopwatch{last: time.Now()} }

func (w *stopwatch) lap(stage string) {
	now := time.Now()
	w.stages = append(w.stages, fmt.Sprintf("%s %s", stage, now.Sub(w.last).Round(time.Millisecond)))
	w.last = now
}

func (w *stopwatch) String() string { return strings.Join(w.stages, ", ") }

func (t *Tunnel) Stop() error {
	t.mu.Lock()
	sess := t.session
	t.session = nil
	t.mu.Unlock()
	if sess == nil {
		return nil
	}
	return t.undo(sess)
}

// Recover undoes a session left behind by a crash.
func (t *Tunnel) Recover() error {
	b, err := os.ReadFile(t.RestorePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var sess session
	if err := json.Unmarshal(b, &sess); err != nil {
		return fmt.Errorf("restore file %s is corrupt: %w", t.RestorePath, err)
	}
	return t.undo(&sess)
}

// undo restores DNS before removing routes or closing the engine, so DNS
// never points at an interface that has already disappeared. Keep the restore
// file after any failure so recovery can retry.
func (t *Tunnel) undo(s *session) error {
	clock := newStopwatch()
	dnsErr := t.setDNS(false)
	clock.lap("dns")
	routeErr := t.setRoutes(false, s)
	clock.lap("routes")
	var engineErr error
	if s.engine != nil {
		engineErr = s.engine.Close()
	}
	clock.lap("engine")
	t.logf("stopped %s: %s", s.Tun, clock)
	if err := errors.Join(dnsErr, routeErr, engineErr); err != nil {
		return err
	}
	if err := os.Remove(t.RestorePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (t *Tunnel) writeRestore(s *session) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(t.RestorePath, b, 0o600)
}

// pinAddress replaces the server's domain with its IP. Once DNS points
// into the tunnel, Xray resolving its own server would need the tunnel to
// be up already. The domain is kept wherever the server sees it: the TLS
// server name and HTTP host headers.
func (t *Tunnel) pinAddress(n node.Node) (node.Node, error) {
	host := n.Address
	if net.ParseIP(host) != nil {
		return n, nil
	}
	ips, err := t.Lookup(host)
	if err != nil || len(ips) == 0 {
		return n, fmt.Errorf("resolve server %s: %v", host, err)
	}
	ip := ips[0]
	for _, candidate := range ips {
		if candidate.To4() != nil {
			ip = candidate
			break
		}
	}
	n.Address = ip.String()
	if n.Security.SNI == "" && (n.Security.Type == "tls" || n.Protocol == "hysteria2") {
		n.Security.SNI = host
	}
	switch n.Transport.Type {
	case "ws", "httpupgrade", "xhttp":
		if n.Transport.Host == "" {
			n.Transport.Host = host
		}
	case "grpc":
		if n.Transport.Authority == "" {
			n.Transport.Authority = host
		}
	}
	return n, nil
}

func logLevel(file string) string {
	if file == "" {
		return ""
	}
	return "info"
}

// usesTCP reports whether Xray reaches the server over TCP, which is what
// CanReach can check. Hysteria2, WireGuard and mKCP run over UDP.
func usesTCP(n node.Node) bool {
	switch {
	case n.Protocol == "hysteria2", n.Protocol == "wireguard", n.Transport.Type == "kcp":
		return false
	case n.Transport.Type == "xhttp" && n.Transport.Mode == "stream-one" && slices.Contains(n.Security.ALPN, "h3"):
		return false
	}
	return true
}
