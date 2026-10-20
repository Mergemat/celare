package system

import (
	"net"
	"strconv"

	"github.com/Mergemat/celare/internal/netconf"
	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
)

// DefaultRoutes reads the IPv4 default routes from the kernel, in the
// order `netstat -rn -f inet` lists them, without starting a process:
// as root under launchd, each command the daemon runs costs it up to
// seconds.
func DefaultRoutes() ([]netconf.Route, error) {
	rib, err := route.FetchRIB(unix.AF_INET, route.RIBTypeRoute, 0)
	if err != nil {
		return nil, err
	}
	msgs, err := route.ParseRIB(route.RIBTypeRoute, rib)
	if err != nil {
		return nil, err
	}
	var out []netconf.Route
	for _, m := range msgs {
		rm, ok := m.(*route.RouteMessage)
		if !ok || rm.Flags&unix.RTF_UP == 0 || len(rm.Addrs) <= unix.RTAX_NETMASK {
			continue
		}
		dst, ok := rm.Addrs[unix.RTAX_DST].(*route.Inet4Addr)
		if !ok || dst.IP != [4]byte{} {
			continue
		}
		if mask, ok := rm.Addrs[unix.RTAX_NETMASK].(*route.Inet4Addr); ok && mask.IP != [4]byte{} {
			continue
		}
		ifi, err := net.InterfaceByIndex(rm.Index)
		if err != nil {
			continue
		}
		r := netconf.Route{Interface: ifi.Name, Scoped: rm.Flags&unix.RTF_IFSCOPE != 0}
		switch gw := rm.Addrs[unix.RTAX_GATEWAY].(type) {
		case *route.Inet4Addr:
			r.Gateway = net.IP(gw.IP[:]).String()
		case *route.LinkAddr:
			r.Gateway = gw.Name
			if r.Gateway == "" {
				r.Gateway = "link#" + strconv.Itoa(gw.Index)
			}
		default:
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// InterfaceNames lists the network interfaces, as `ifconfig -l` does.
func InterfaceNames() ([]string, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	names := make([]string, len(ifs))
	for i, ifi := range ifs {
		names[i] = ifi.Name
	}
	return names, nil
}
