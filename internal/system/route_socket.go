package system

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"slices"
	"sync/atomic"
	"time"

	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
)

// RouteSpec describes a route independently of the routing socket protocol.
type RouteSpec struct {
	Prefix    netip.Prefix `json:"prefix"`
	Interface string       `json:"interface"`
	Gateway   string       `json:"gateway,omitempty"`
	Scoped    bool         `json:"scoped,omitempty"`
}

var routeSequence atomic.Int32

func tunnelRoutes(tun string) []RouteSpec {
	var out []RouteSpec
	for _, prefix := range []string{"0.0.0.0/1", "128.0.0.0/1", "::/1", "8000::/1"} {
		out = append(out, RouteSpec{Prefix: netip.MustParsePrefix(prefix), Interface: tun})
	}
	return out
}

func routeMessage(add bool, spec RouteSpec, index int) (*route.RouteMessage, error) {
	if !spec.Prefix.IsValid() || spec.Prefix != spec.Prefix.Masked() {
		return nil, fmt.Errorf("invalid route prefix %s", spec.Prefix)
	}
	m := &route.RouteMessage{
		Version: unix.RTM_VERSION, Type: unix.RTM_DELETE,
		Flags: unix.RTF_UP | unix.RTF_STATIC, Index: index,
		ID: uintptr(os.Getpid()), Seq: 1,
		Addrs: make([]route.Addr, unix.RTAX_MAX),
	}
	if add {
		m.Type = unix.RTM_ADD
	}
	if spec.Scoped {
		m.Flags |= unix.RTF_IFSCOPE
	}
	ip := spec.Prefix.Addr()
	mask := net.CIDRMask(spec.Prefix.Bits(), ip.BitLen())
	if ip.Is4() {
		m.Addrs[unix.RTAX_DST] = &route.Inet4Addr{IP: ip.As4()}
		m.Addrs[unix.RTAX_NETMASK] = &route.Inet4Addr{IP: [4]byte(mask)}
	} else {
		m.Addrs[unix.RTAX_DST] = &route.Inet6Addr{IP: ip.As16()}
		m.Addrs[unix.RTAX_NETMASK] = &route.Inet6Addr{IP: [16]byte(mask)}
	}
	if spec.Gateway != "" {
		gw, err := netip.ParseAddr(spec.Gateway)
		if err != nil || gw.Is4() != ip.Is4() {
			return nil, fmt.Errorf("invalid gateway %q for %s", spec.Gateway, spec.Prefix)
		}
		m.Flags |= unix.RTF_GATEWAY
		if gw.Is4() {
			m.Addrs[unix.RTAX_GATEWAY] = &route.Inet4Addr{IP: gw.As4()}
		} else {
			zone := 0
			if gw.IsLinkLocalUnicast() || gw.Zone() != "" {
				zone = index
			}
			m.Addrs[unix.RTAX_GATEWAY] = &route.Inet6Addr{IP: gw.As16(), ZoneID: zone}
		}
	} else {
		m.Addrs[unix.RTAX_GATEWAY] = &route.LinkAddr{Index: index, Name: spec.Interface}
	}
	return m, nil
}

// ChangeRoute writes directly to the kernel and checks its acknowledgement.
// Each call owns its socket, so replies from simultaneous changes cannot mix.
func ChangeRoute(add bool, spec RouteSpec) error {
	ifi, err := net.InterfaceByName(spec.Interface)
	if err != nil {
		// Closing an engine or a crashed process already removes its routes.
		if !add {
			if names, listErr := InterfaceNames(); listErr == nil && !slices.Contains(names, spec.Interface) {
				return nil
			}
		}
		return err
	}
	m, err := routeMessage(add, spec, ifi.Index)
	if err != nil {
		return err
	}
	m.Seq = int(routeSequence.Add(1))
	b, err := m.Marshal()
	if err != nil {
		return err
	}
	fd, err := unix.Socket(unix.AF_ROUTE, unix.SOCK_RAW, unix.AF_UNSPEC)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	unix.CloseOnExec(fd)
	// A missing acknowledgement must not leave a transition stuck forever.
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &unix.Timeval{Sec: 2}); err != nil {
		return err
	}
	if _, err := unix.Write(fd, b); err != nil {
		if !add && errors.Is(err, unix.ESRCH) {
			return nil
		}
		return err
	}
	buf := make([]byte, 64*1024)
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		n, err := unix.Read(fd, buf)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return err
		}
		msgs, err := route.ParseRIB(route.RIBTypeRoute, buf[:n])
		if err != nil {
			return err
		}
		for _, msg := range msgs {
			if reply, ok := msg.(*route.RouteMessage); ok && reply.ID == m.ID && reply.Seq == m.Seq && reply.Type == m.Type {
				if !add && errors.Is(reply.Err, unix.ESRCH) {
					return nil
				}
				return reply.Err
			}
		}
	}
	return errors.New("timed out waiting for the route acknowledgement")
}
