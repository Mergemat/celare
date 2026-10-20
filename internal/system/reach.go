package system

import (
	"net"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// DialBound opens and closes a TCP connection to host:port that may only
// leave through iface, as Xray's bound outbound does. It finds firewalls
// that refuse such traffic before the tunnel depends on it.
func DialBound(host string, port int, iface string) error {
	ifi, err := net.InterfaceByName(iface)
	if err != nil {
		return err
	}
	d := net.Dialer{
		Timeout: 4 * time.Second,
		Control: func(network, _ string, c syscall.RawConn) error {
			var bindErr error
			err := c.Control(func(fd uintptr) {
				if strings.HasSuffix(network, "6") {
					bindErr = unix.SetsockoptInt(int(fd), unix.IPPROTO_IPV6, unix.IPV6_BOUND_IF, ifi.Index)
				} else {
					bindErr = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_BOUND_IF, ifi.Index)
				}
			})
			if err != nil {
				return err
			}
			return bindErr
		},
	}
	conn, err := d.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return err
	}
	return conn.Close()
}
