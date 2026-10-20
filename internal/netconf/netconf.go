// Package netconf turns macOS routing and DNS into data: it parses the
// output of netstat and networksetup and builds the commands that point
// the system at the tunnel and back. Running them is the caller's job.
package netconf

import (
	"regexp"
	"strconv"
	"strings"
)

// TunnelDNS is the resolver address the system is pointed at while the
// tunnel is up. It sits inside 128.0.0.0/1, so queries enter the tunnel,
// where Xray answers everything on port 53 itself.
const TunnelDNS = "198.18.0.53"

// Route is one IPv4 default route.
type Route struct {
	Gateway   string
	Interface string
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
		out = append(out, Route{Gateway: f[1], Interface: f[3]})
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

// ForeignTunnels lists tunnel interfaces other than ours that hold a
// default route; another VPN running alongside celare fights it for
// traffic.
func ForeignTunnels(routes []Route, ours string) []string {
	var out []string
	for _, r := range routes {
		if isTunnel(r.Interface) && r.Interface != ours {
			out = append(out, r.Interface)
		}
	}
	return out
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
