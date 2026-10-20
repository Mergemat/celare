// Package netconf turns macOS routing and DNS into data: it parses the
// output of netstat and networksetup and builds the commands that point
// the system at the tunnel and back. Running them is the caller's job.
package netconf

import (
	"net"
	"regexp"
	"strconv"
	"strings"
)

// TunnelDNS is the resolver address the system is pointed at while the
// tunnel is up: Xray's end of the tunnel's point-to-point link (Xray
// gives the Mac 169.254.10.2 and itself .1), so queries always enter
// celare's tunnel, where Xray answers everything on port 53. It must stay
// clear of 198.18.0.0/15, which VPN apps such as V2Box use for fake IPs.
const TunnelDNS = "169.254.10.1"

// LegacyTunnelDNS is what celare 0.1 set; doctor still recognises it.
const LegacyTunnelDNS = "198.18.0.53"

// Route is one IPv4 default route.
type Route struct {
	Gateway   string
	Interface string
	// Scoped routes (flag I) only serve sockets bound to their
	// interface; the unscoped one is the system's primary.
	Scoped bool
}

// Service is a macOS network service ("Wi-Fi") and its device ("en0").
type Service struct {
	Name     string
	Device   string
	Disabled bool
}

// ParseDefaultRoutes reads the default routes from `netstat -rn -f inet`.
func ParseDefaultRoutes(netstat string) []Route {
	var out []Route
	for _, line := range strings.Split(netstat, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || f[0] != "default" {
			continue
		}
		out = append(out, Route{Gateway: f[1], Interface: f[3], Scoped: strings.Contains(f[2], "I")})
	}
	return out
}

// isTunnel reports whether an interface belongs to some VPN rather than
// to physical hardware.
func isTunnel(iface string) bool {
	for _, p := range []string{"utun", "ipsec", "ppp", "tun", "tap", "gif", "stf"} {
		if strings.HasPrefix(iface, p) {
			return true
		}
	}
	return false
}

// PhysicalDefault returns the default route through real hardware, even
// when a VPN has put its own default route first.
func PhysicalDefault(routes []Route) (Route, bool) {
	for _, r := range routes {
		if !isTunnel(r.Interface) && !strings.HasPrefix(r.Gateway, "link#") {
			return r, true
		}
	}
	return Route{}, false
}

// Upstream is the route celare's own traffic to its server takes: the
// system's current primary default route, whichever interface that is
// (Wi-Fi, Ethernet, or another VPN's tunnel), never celare's own tunnel.
func Upstream(routes []Route, ours string) (Route, bool) {
	var scoped []Route
	for _, r := range routes {
		if r.Interface == ours && ours != "" {
			continue
		}
		if r.Scoped {
			scoped = append(scoped, r)
			continue
		}
		return r, true
	}
	if len(scoped) > 0 {
		return scoped[0], true
	}
	return Route{}, false
}

// HasScopedDefault reports whether iface already has its own scoped
// default route; macOS adds one for every network service but the primary.
func HasScopedDefault(routes []Route, iface string) bool {
	for _, r := range routes {
		if r.Scoped && r.Interface == iface {
			return true
		}
	}
	return false
}

// ScopedDefault is the route command (op "add" or "delete") for a default
// route scoped to up's interface. Xray's outbounds are bound to that
// interface (IP_BOUND_IF), so their lookups only accept routes through
// it; the primary interface has no scoped default, so once the tunnel's
// half-default routes are in, they would be sent back into the tunnel.
func ScopedDefault(op string, up Route) []string {
	cmd := []string{"route", "-n", op, "-ifscope", up.Interface, "default"}
	if net.ParseIP(up.Gateway) == nil {
		return append(cmd, "-interface", up.Interface)
	}
	return append(cmd, up.Gateway)
}

var (
	serviceLine = regexp.MustCompile(`^\((\d+|\*)\) (.+)$`)
	deviceLine  = regexp.MustCompile(`Device: ([^)]*)\)`)
)

// ParseServiceOrder reads `networksetup -listnetworkserviceorder`.
func ParseServiceOrder(out string) []Service {
	var services []Service
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if m := serviceLine.FindStringSubmatch(line); m != nil {
			services = append(services, Service{Name: m[2], Disabled: m[1] == "*"})
			continue
		}
		if m := deviceLine.FindStringSubmatch(line); m != nil && len(services) > 0 {
			services[len(services)-1].Device = strings.TrimSpace(m[1])
		}
	}
	return services
}

// ServiceForDevice finds the enabled service that owns a device.
func ServiceForDevice(services []Service, device string) (string, bool) {
	for _, s := range services {
		if s.Device == device && !s.Disabled {
			return s.Name, true
		}
	}
	return "", false
}

// ParseDNSServers reads `networksetup -getdnsservers <service>`. No
// servers means the service takes DNS from DHCP.
func ParseDNSServers(out string) []string {
	var servers []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, " ") {
			continue
		}
		servers = append(servers, line)
	}
	return servers
}

// halfDefaults are more specific than 0.0.0.0/0 and ::/0, so they win
// over the physical default route without touching it.
var halfDefaults = []struct{ family, net string }{
	{"", "0.0.0.0/1"},
	{"", "128.0.0.0/1"},
	{"-inet6", "::/1"},
	{"-inet6", "8000::/1"},
}

func routeCmd(op, family, net, tun string) []string {
	cmd := []string{"route", "-n", op}
	if family != "" {
		cmd = append(cmd, family)
	}
	return append(cmd, "-net", net, "-interface", tun)
}

// UpCommands routes all traffic into tun and points service's DNS at the
// tunnel resolver.
func UpCommands(tun, service string) [][]string {
	var cmds [][]string
	for _, h := range halfDefaults {
		cmds = append(cmds, routeCmd("add", h.family, h.net, tun))
	}
	return append(cmds, []string{"networksetup", "-setdnsservers", service, TunnelDNS})
}

// DownCommands undoes UpCommands, restoring the DNS servers saved before
// the tunnel came up (none means back to DHCP).
func DownCommands(tun, service string, savedDNS []string) [][]string {
	var cmds [][]string
	for _, h := range halfDefaults {
		cmds = append(cmds, routeCmd("delete", h.family, h.net, tun))
	}
	dns := []string{"networksetup", "-setdnsservers", service}
	if len(savedDNS) == 0 {
		dns = append(dns, "Empty")
	} else {
		dns = append(dns, savedDNS...)
	}
	return append(cmds, dns)
}

// FreeTunName picks a utun name above every existing one, so it can't
// collide with interfaces the system or other VPNs create.
func FreeTunName(existing []string) string {
	highest := -1
	for _, name := range existing {
		if n, err := strconv.Atoi(strings.TrimPrefix(name, "utun")); err == nil && strings.HasPrefix(name, "utun") && n > highest {
			highest = n
		}
	}
	return "utun" + strconv.Itoa(highest+1)
}

var ncLine = regexp.MustCompile(`^\*?\s*\((\w+)\)\s+\S+\s+VPN\s+\([^)]*\)\s+"([^"]+)"`)

// ConnectedVPNs reads `scutil --nc list` and returns the names of VPN
// apps that are connected right now, e.g. "V2BOX". Each can be turned off
// with `scutil --nc stop <name>`.
func ConnectedVPNs(out string) []string {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		if m := ncLine.FindStringSubmatch(strings.TrimSpace(line)); m != nil && m[1] == "Connected" {
			names = append(names, m[2])
		}
	}
	return names
}
