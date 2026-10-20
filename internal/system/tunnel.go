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

// Tunnel implements daemon.Tunnel.
type Tunnel struct {
	// Run executes a command and returns its combined output.
	Run func(name string, args ...string) (string, error)
	// StartEngine starts Xray with a JSON config.
	StartEngine func(configJSON []byte) (io.Closer, error)
	// Lookup resolves the server's domain before DNS moves into the tunnel.
	Lookup func(host string) ([]net.IP, error)
	// XrayLog, if set, is where the tunnel's Xray logs at info level,
	// truncated at each start.
	XrayLog string
	// CanReach opens a TCP connection to host:port bound to iface, the way
	// Xray will reach the server once the tunnel is up.
	CanReach    func(host string, port int, iface string) error
	RestorePath string
	// Routes and Interfaces read the system without starting a process;
	// nil falls back to netstat and ifconfig through Run.
	Routes     func() ([]netconf.Route, error)
	Interfaces func() ([]string, error)
	// Logf, if set, gets a line per start and stop saying where the time
	// went.
	Logf func(format string, args ...any)

	// mu guards session: status reads it while the daemon changes it.
	mu      sync.Mutex
	session *session
}

// session is what Stop needs to undo Start; it is also the restore file.
type session struct {
	Tun       string   `json:"tun"`
	Service   string   `json:"service"`
	Interface string   `json:"interface"`
	SavedDNS  []string `json:"savedDns"`
	// ScopedRoute deletes the upstream's scoped default route if celare
	// added it.
	ScopedRoute []string `json:"scopedRoute,omitempty"`
	engine      io.Closer
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
	out, err := t.Run("netstat", "-rn", "-f", "inet")
	if err != nil {
		return nil, fmt.Errorf("read routes: %w", err)
	}
	return netconf.ParseDefaultRoutes(out), nil
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
	order, err := t.Run("networksetup", "-listnetworkserviceorder")
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
	// DNS is set on the physical network service (Wi-Fi, Ethernet); the
	// server connection leaves by the upstream, which is another VPN's
	// tunnel when one is connected.
	routes, err := t.routes()
	if err != nil {
		return err
	}
	up, err := upstream(routes, "")
	if err != nil {
		return err
	}
	n, err := t.pinAddress(s.Node)
	if err != nil {
		return err
	}
	// Each of these costs a round trip or a process, so the server check
	// runs while the network service is read.
	reached := make(chan error, 1)
	go func() { reached <- t.reach(n, up.Interface) }()
	_, service, err := t.physical(routes)
	var dnsOut string
	if err == nil {
		if dnsOut, err = t.Run("networksetup", "-getdnsservers", service); err != nil {
			err = fmt.Errorf("read DNS of %s: %w", service, err)
		}
	}
	reachErr := <-reached
	if err != nil {
		return err
	}
	if reachErr != nil {
		return fmt.Errorf("can't reach the server %s:%d over %s: %v", n.Address, n.Port, up.Interface, reachErr)
	}
	ifaces, err := t.interfaceNames()
	if err != nil {
		return fmt.Errorf("list interfaces: %w", err)
	}
	sess := &session{
		Tun:       netconf.FreeTunName(ifaces),
		Service:   service,
		Interface: up.Interface,
		SavedDNS:  netconf.ParseDNSServers(dnsOut),
	}
	if !netconf.HasScopedDefault(routes, up.Interface) {
		sess.ScopedRoute = netconf.ScopedDefault("delete", up)
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

	cmds := netconf.UpCommands(sess.Tun, sess.Service)
	if sess.ScopedRoute != nil {
		cmds = append(cmds, netconf.ScopedDefault("add", up))
	}
	if err := errors.Join(t.runAll(cmds)...); err != nil {
		t.undo(sess)
		return fmt.Errorf("%w; changes undone", err)
	}
	clock.lap("routes")
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
	out, err := t.Run("ifconfig", "-l")
	return strings.Fields(out), err
}

// runAll runs commands that don't depend on each other at once: starting
// a process costs the daemon up to a second. The result has an error for
// each command that failed, nil for the rest.
func (t *Tunnel) runAll(cmds [][]string) []error {
	errs := make([]error, len(cmds))
	var wg sync.WaitGroup
	for i, cmd := range cmds {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if out, err := t.Run(cmd[0], cmd[1:]...); err != nil {
				errs[i] = fmt.Errorf("%s: %s (%w)", strings.Join(cmd, " "), strings.TrimSpace(out), err)
			}
		}()
	}
	wg.Wait()
	return errs
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

// undo reverts everything Start may have changed. It keeps going past
// failures (a route may already be gone) and reports the DNS one, which
// is the one that leaves the machine offline.
func (t *Tunnel) undo(s *session) error {
	clock := newStopwatch()
	var dnsErr error
	cmds := netconf.DownCommands(s.Tun, s.Service, s.SavedDNS)
	if s.ScopedRoute != nil {
		cmds = append(cmds, s.ScopedRoute)
	}
	for i, err := range t.runAll(cmds) {
		if err != nil && cmds[i][0] == "networksetup" {
			dnsErr = fmt.Errorf("restore DNS of %s: %w", s.Service, err)
		}
	}
	clock.lap("routes")
	if s.engine != nil {
		s.engine.Close()
	}
	clock.lap("engine")
	t.logf("stopped %s: %s", s.Tun, clock)
	if err := os.Remove(t.RestorePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.Join(dnsErr, err)
	}
	return dnsErr
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
