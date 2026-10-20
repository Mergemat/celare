package netconf_test

import (
	"testing"

	"github.com/Mergemat/celare/internal/netconf"
	"github.com/google/go-cmp/cmp"
)

// Captured from `netstat -rn -f inet` on a Mac where another VPN (utun4)
// owns the default route.
const netstatWithOtherVPN = `Routing tables

Internet:
Destination        Gateway            Flags               Netif Expire
default            link#29            UCSg                utun4
default            192.168.1.1        UGScIg                en0
127                127.0.0.1          UCS                   lo0
192.168.1.11/32    link#15            UCS                   en0      !
`

func TestPhysicalDefaultSkipsTunnels(t *testing.T) {
	got, ok := netconf.PhysicalDefault(netconf.ParseDefaultRoutes(netstatWithOtherVPN))
	if !ok {
		t.Fatal("found no physical default route")
	}
	want := netconf.Route{Gateway: "192.168.1.1", Interface: "en0", Scoped: true}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("PhysicalDefault mismatch (-want +got):\n%s", diff)
	}
}

// Captured from `networksetup -listnetworkserviceorder`, trimmed.
const serviceOrder = `An asterisk (*) denotes that a network service is disabled.
(1) USB 10/100 LAN
(Hardware Port: USB 10/100 LAN, Device: en9)

(2) Wi-Fi
(Hardware Port: Wi-Fi, Device: en0)

(*) Thunderbolt Bridge
(Hardware Port: Thunderbolt Bridge, Device: bridge0)

(12) V2BOX
(Hardware Port: hossin.asaadi.V2Box, Device: )
`

func TestServiceForDevice(t *testing.T) {
	services := netconf.ParseServiceOrder(serviceOrder)
	got, ok := netconf.ServiceForDevice(services, "en0")
	if !ok || got != "Wi-Fi" {
		t.Errorf("ServiceForDevice(en0) = %q, %v; want Wi-Fi", got, ok)
	}
	if _, ok := netconf.ServiceForDevice(services, "bridge0"); ok {
		t.Error("a disabled service was returned for bridge0")
	}
}

func TestParseDNSServers(t *testing.T) {
	if diff := cmp.Diff([]string{"1.1.1.1", "8.8.8.8"}, netconf.ParseDNSServers("1.1.1.1\n8.8.8.8\n")); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
	// networksetup's wording when DNS comes from DHCP.
	if got := netconf.ParseDNSServers("There aren't any DNS Servers set on Wi-Fi.\n"); len(got) != 0 {
		t.Errorf("DHCP-provided DNS parsed as %v, want none", got)
	}
}

func TestFreeTunName(t *testing.T) {
	got := netconf.FreeTunName([]string{"lo0", "en0", "utun0", "utun1", "utun3", "utun100"})
	if got != "utun101" {
		t.Errorf("FreeTunName = %q, want utun101 (above every existing utun)", got)
	}
}

// Captured from `scutil --nc list` with V2Box connected.
const ncList = `Available network connection services in the current set (*=enabled):
* (Disconnected)   F05D7F50-A8D5-45DE-8442-1E45F4E46BDB VPN (io.tailscale.ipn.macsys) "Tailscale"                      [VPN:io.tailscale.ipn.macsys]
* (Connected)      2429CEEB-441A-4C22-A4FE-23A588042933 VPN (hossin.asaadi.V2Box) "V2BOX"                          [VPN:hossin.asaadi.V2Box]
`

func TestConnectedVPNsByName(t *testing.T) {
	if diff := cmp.Diff([]string{"V2BOX"}, netconf.ConnectedVPNs(ncList)); diff != "" {
		t.Errorf("ConnectedVPNs mismatch (-want +got):\n%s", diff)
	}
}

// With V2Box connected its tunnel is the primary default route and en0's
// default is scoped (the I flag). celare must go out through V2Box, the
// way every other app's traffic does.
const netstatV2BoxPrimary = `Internet:
Destination        Gateway            Flags               Netif Expire
default            link#30            UCSg                utun4
default            192.168.1.1        UGScIg                en0
0/1                utun9              UScg                utun9
`

func TestUpstreamFollowsTheSystemsPrimaryRoute(t *testing.T) {
	routes := netconf.ParseDefaultRoutes(netstatV2BoxPrimary)
	got, ok := netconf.Upstream(routes, "utun9")
	if !ok || got.Interface != "utun4" {
		t.Errorf("Upstream = %+v, %v; want utun4 (V2Box)", got, ok)
	}
	got, ok = netconf.Upstream(netconf.ParseDefaultRoutes(netstatWithOtherVPN), "")
	if !ok || got.Interface != "utun4" {
		t.Errorf("Upstream = %+v, %v; want utun4", got, ok)
	}
	plain := "Internet:\ndefault            192.168.1.1        UGScg                 en0\n"
	got, ok = netconf.Upstream(netconf.ParseDefaultRoutes(plain), "")
	if !ok || got.Interface != "en0" {
		t.Errorf("Upstream = %+v, %v; want en0 when no other VPN is up", got, ok)
	}
}

// celare's own tunnel never counts as the way out, even if it shows up
// as a default route.
func TestUpstreamSkipsCelaresOwnTunnel(t *testing.T) {
	routes := netconf.ParseDefaultRoutes("Internet:\ndefault  link#40  UCSg  utun9\ndefault  192.168.1.1  UGScIg  en0\n")
	got, ok := netconf.Upstream(routes, "utun9")
	if !ok || got.Interface != "en0" {
		t.Errorf("Upstream = %+v, %v; want en0", got, ok)
	}
}
