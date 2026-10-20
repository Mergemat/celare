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
	"strings"

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
	Lookup      func(host string) ([]net.IP, error)
	RestorePath string

	session *session
}

// session is what Stop needs to undo Start; it is also the restore file.
type session struct {
	Tun       string   `json:"tun"`
	Service   string   `json:"service"`
	Interface string   `json:"interface"`
	SavedDNS  []string `json:"savedDns"`
	engine    io.Closer
}

// Exec runs a command for real.
func Exec(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}

// Physical reports the physical default route and its network service.
func (t *Tunnel) Physical() (route netconf.Route, service string, err error) {
	out, err := t.Run("netstat", "-rn", "-f", "inet")
	if err != nil {
		return netconf.Route{}, "", fmt.Errorf("read routes: %w", err)
	}
	route, ok := netconf.PhysicalDefault(netconf.ParseDefaultRoutes(out))
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

// ForeignTunnels lists other VPN interfaces holding a default route.
func (t *Tunnel) ForeignTunnels() []string {
	out, err := t.Run("netstat", "-rn", "-f", "inet")
	if err != nil {
		return nil
	}
	ours := ""
	if t.session != nil {
		ours = t.session.Tun
	}
	return netconf.ForeignTunnels(netconf.ParseDefaultRoutes(out), ours)
}

// Interface is the physical interface the running tunnel is bound to.
func (t *Tunnel) Interface() string {
	if t.session == nil {
		return ""
	}
	return t.session.Interface
}

// TunName is the utun interface of the running tunnel.
func (t *Tunnel) TunName() string {
	if t.session == nil {
		return ""
	}
	return t.session.Tun
}

func (t *Tunnel) Start(s daemon.Spec) error {
	if t.session != nil {
		return errors.New("tunnel already running")
	}
	route, service, err := t.Physical()
	if err != nil {
		return err
	}
	dnsOut, err := t.Run("networksetup", "-getdnsservers", service)
	if err != nil {
		return fmt.Errorf("read DNS of %s: %w", service, err)
	}
	ifaces, err := t.Run("ifconfig", "-l")
	if err != nil {
		return fmt.Errorf("list interfaces: %w", err)
	}
	sess := &session{
		Tun:       netconf.FreeTunName(strings.Fields(ifaces)),
		Service:   service,
		Interface: route.Interface,
		SavedDNS:  netconf.ParseDNSServers(dnsOut),
	}

	n, err := t.pinAddress(s.Node)
	if err != nil {
		return err
	}
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
		BindInterface: route.Interface,
	})
	if err != nil {
		return err
	}
	if sess.engine, err = t.StartEngine(cfg); err != nil {
		return err
	}
	if err := t.writeRestore(sess); err != nil {
		sess.engine.Close()
		return fmt.Errorf("write restore file: %w", err)
	}
	for _, cmd := range netconf.UpCommands(sess.Tun, sess.Service) {
		if out, err := t.Run(cmd[0], cmd[1:]...); err != nil {
			t.undo(sess)
			return fmt.Errorf("%s: %s (%w); changes undone", strings.Join(cmd, " "), strings.TrimSpace(out), err)
		}
	}
	t.session = sess
	return nil
}

func (t *Tunnel) Stop() error {
	if t.session == nil {
		return nil
	}
	err := t.undo(t.session)
	t.session = nil
	return err
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
	var dnsErr error
	for _, cmd := range netconf.DownCommands(s.Tun, s.Service, s.SavedDNS) {
		out, err := t.Run(cmd[0], cmd[1:]...)
		if err != nil && cmd[0] == "networksetup" {
			dnsErr = fmt.Errorf("restore DNS of %s: %s (%w)", s.Service, strings.TrimSpace(out), err)
		}
	}
	if s.engine != nil {
		s.engine.Close()
	}
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
