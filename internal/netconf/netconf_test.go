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
	want := netconf.Route{Gateway: "192.168.1.1", Interface: "en0"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("PhysicalDefault mismatch (-want +got):\n%s", diff)
	}
}

func TestTunnelOwningDefaultIsReported(t *testing.T) {
	got := netconf.ForeignTunnels(netconf.ParseDefaultRoutes(netstatWithOtherVPN), "utun9")
	if diff := cmp.Diff([]string{"utun4"}, got); diff != "" {
		t.Errorf("ForeignTunnels mismatch (-want +got):\n%s", diff)
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

// Half-default routes beat the real default without replacing it, so
// taking the tunnel down only means deleting them.
func TestUpAndDownCommands(t *testing.T) {
	up := netconf.UpCommands("utun9", "Wi-Fi")
	wantUp := [][]string{
		{"route", "-n", "add", "-net", "0.0.0.0/1", "-interface", "utun9"},
		{"route", "-n", "add", "-net", "128.0.0.0/1", "-interface", "utun9"},
		{"route", "-n", "add", "-inet6", "-net", "::/1", "-interface", "utun9"},
		{"route", "-n", "add", "-inet6", "-net", "8000::/1", "-interface", "utun9"},
		{"networksetup", "-setdnsservers", "Wi-Fi", netconf.TunnelDNS},
	}
	if diff := cmp.Diff(wantUp, up); diff != "" {
		t.Errorf("UpCommands mismatch (-want +got):\n%s", diff)
	}
	down := netconf.DownCommands("utun9", "Wi-Fi", nil)
	wantDown := [][]string{
		{"route", "-n", "delete", "-net", "0.0.0.0/1", "-interface", "utun9"},
		{"route", "-n", "delete", "-net", "128.0.0.0/1", "-interface", "utun9"},
		{"route", "-n", "delete", "-inet6", "-net", "::/1", "-interface", "utun9"},
		{"route", "-n", "delete", "-inet6", "-net", "8000::/1", "-interface", "utun9"},
		{"networksetup", "-setdnsservers", "Wi-Fi", "Empty"},
	}
	if diff := cmp.Diff(wantDown, down); diff != "" {
		t.Errorf("DownCommands mismatch (-want +got):\n%s", diff)
	}
	restore := netconf.DownCommands("utun9", "Wi-Fi", []string{"1.1.1.1", "8.8.8.8"})
	if diff := cmp.Diff([]string{"networksetup", "-setdnsservers", "Wi-Fi", "1.1.1.1", "8.8.8.8"}, restore[len(restore)-1]); diff != "" {
		t.Errorf("DNS restore mismatch (-want +got):\n%s", diff)
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
