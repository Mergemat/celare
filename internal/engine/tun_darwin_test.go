//go:build darwin

package engine_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"runtime"
	"slices"
	"strconv"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/sys/unix"
	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/tun/netstack"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"

	"github.com/Mergemat/celare/internal/config"
	"github.com/Mergemat/celare/internal/node"
	"github.com/Mergemat/celare/internal/xrayconf"
)

// Every app's packets enter celare through Xray's TUN inbound. Xray also
// takes a ready utun descriptor (that is how it runs on iOS), so here a
// socketpair in utun's framing stands in for the utun and a userspace
// TCP/IP stack plays the apps. The real packet path runs without
// touching any interface, route or DNS setting.

var (
	appV4  = netip.MustParseAddr("169.254.10.2")
	appV6  = netip.MustParseAddr("fd00:ce1a::2")
	tunDNS = netip.MustParseAddr("169.254.10.1") // where celare points system DNS
	siteV4 = netip.MustParseAddr("198.51.100.7")
	siteV6 = netip.MustParseAddr("2001:db8::7")
	// rawV4 belongs to no app stack: the test speaks TCP for it by hand.
	rawV4 = netip.MustParseAddr("169.254.10.3")
)

const bulkSize = 16 << 20

func TestTUNCarriesAppTraffic(t *testing.T) {
	apps := startTUN(t, startUpstream(t))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// DNS is hijacked into Xray and answered through the proxy.
	addrs, err := apps.LookupContextHost(ctx, "example.test")
	if err != nil || !slices.Contains(addrs, siteV4.String()) || !slices.Contains(addrs, siteV6.String()) {
		t.Fatalf("lookup through the tunnel = %v, %v; want %s and %s", addrs, err, siteV4, siteV6)
	}
	client := appClient(apps)
	for _, u := range []string{"http://example.test/", "http://" + siteV4.String() + "/", "http://[" + siteV6.String() + "]/"} {
		if got := get(t, client, u); got != "proxied" {
			t.Errorf("%s through the tunnel answered %q, want proxied", u, got)
		}
	}

	conn, err := apps.DialUDPAddrPort(netip.AddrPort{}, netip.AddrPortFrom(siteV4, 7))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 64)
	if n, err := conn.Read(b); err != nil || string(b[:n]) != "ping" {
		t.Errorf("UDP echo through the tunnel = %q, %v; want ping", b[:n], err)
	}

	resp, err := client.Get("http://example.test/bulk")
	if err != nil {
		t.Fatal(err)
	}
	n, err := io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if err != nil || n != bulkSize {
		t.Errorf("bulk download through the tunnel got %d bytes, %v; want %d", n, err, bulkSize)
	}
}

// Port probes and aborted connects complete the handshake and reset the
// connection at once. Xray 26.3's TUN stack panicked when the reset won
// the race with the new connection's handler, which killed the engine
// and left the tunnel dead until a reconnect.
func TestTUNSurvivesConnectionsResetAtOnce(t *testing.T) {
	apps, wire := startTUNWire(t, startUpstream(t))
	client := appClient(apps)
	if got := get(t, client, "http://example.test/"); got != "proxied" {
		t.Fatalf("tunnel answered %q before the burst, want proxied", got)
	}
	settle := func() int {
		runtime.GC()
		time.Sleep(100 * time.Millisecond)
		return runtime.NumGoroutine()
	}
	before := settle()

	const probes = 2000
	dst := netip.AddrPortFrom(siteV4, 443)
	answered := make(chan int)
	go func() {
		n := 0
		defer func() { answered <- n }()
		for n < probes {
			var seg header.TCP
			select {
			case pkt := <-wire.raw:
				seg = header.TCP(header.IPv4(pkt).Payload())
			case <-time.After(10 * time.Second):
				return
			}
			if seg.Flags() != header.TCPFlagSyn|header.TCPFlagAck {
				continue
			}
			src := netip.AddrPortFrom(rawV4, seg.DestinationPort())
			ack := seg.SequenceNumber() + 1
			wire.write(t, tcpPacket(src, dst, 1001, ack, header.TCPFlagAck))
			wire.write(t, tcpPacket(src, dst, 1001, ack, header.TCPFlagRst|header.TCPFlagAck))
			n++
		}
	}()
	for i := range probes {
		wire.write(t, tcpPacket(netip.AddrPortFrom(rawV4, uint16(20000+i)), dst, 1000, 0, header.TCPFlagSyn))
	}
	if n := <-answered; n != probes {
		t.Fatalf("%d of %d probes got through the handshake", n, probes)
	}

	if got := get(t, client, "http://example.test/"); got != "proxied" {
		t.Errorf("tunnel answered %q after the burst, want proxied", got)
	}
	deadline := time.Now().Add(15 * time.Second)
	after := settle()
	for after > before+20 && time.Now().Before(deadline) {
		after = settle()
	}
	if after > before+20 {
		t.Errorf("%d goroutines before the burst, %d after: reset connections are leaking", before, after)
	}
}

func BenchmarkTUNDownload(b *testing.B) {
	client := appClient(startTUN(b, startUpstream(b)))
	b.SetBytes(bulkSize)
	for b.Loop() {
		resp, err := client.Get("http://example.test/bulk")
		if err != nil {
			b.Fatal(err)
		}
		if n, err := io.Copy(io.Discard, resp.Body); err != nil || n != bulkSize {
			b.Fatalf("got %d bytes, %v", n, err)
		}
		resp.Body.Close()
	}
}

// BenchmarkTUNRequest is one short request on a new connection: what
// opening a page costs per resource.
func BenchmarkTUNRequest(b *testing.B) {
	client := appClient(startTUN(b, startUpstream(b)))
	for b.Loop() {
		if got := get(b, client, "http://"+siteV4.String()+"/"); got != "proxied" {
			b.Fatalf("answered %q", got)
		}
	}
}

// startUpstream runs the server side: a VLESS server that sends DNS to a
// resolver answering siteV4/siteV6 for every name, other UDP to an echo
// server, and everything else to a web server. It returns its port.
func startUpstream(t testing.TB) int {
	t.Helper()
	bulk := make([]byte, bulkSize)
	web := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bulk" {
			w.Header().Set("Content-Length", strconv.Itoa(len(bulk)))
			w.Write(bulk)
			return
		}
		io.WriteString(w, "proxied")
	})}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go web.Serve(l)
	t.Cleanup(func() { web.Close() })

	port := freePort(t)
	startEngine(t, fmt.Sprintf(`{
		"inbounds": [{"listen": "127.0.0.1", "port": %d, "protocol": "vless",
			"settings": {"clients": [{"id": %q}], "decryption": "none"}}],
		"outbounds": [
			{"tag": "web", "protocol": "freedom", "settings": {"redirect": %q, "finalRules": [{"action": "allow"}]}},
			{"tag": "dns", "protocol": "freedom", "settings": {"redirect": %q, "finalRules": [{"action": "allow"}]}},
			{"tag": "echo", "protocol": "freedom", "settings": {"redirect": %q, "finalRules": [{"action": "allow"}]}}
		],
		"routing": {"rules": [
			{"port": "53", "outboundTag": "dns"},
			{"network": "udp", "outboundTag": "echo"}
		]}
	}`, port, testUUID, l.Addr().String(), serveUDP(t, answerDNS), serveUDP(t, echo)))
	return port
}

// startTUN brings up celare's tunnel config on a stand-in utun and
// returns the apps' network.
func startTUN(t testing.TB, serverPort int) *netstack.Net {
	apps, _ := startTUNWire(t, serverPort)
	return apps
}

// utun is the apps' end of the stand-in utun.
type utun struct {
	file *os.File
	// raw gets the IPv4 packets Xray sends to rawV4.
	raw chan []byte
}

// startTUNWire also returns the apps' end of the utun, for raw packets.
func startTUNWire(t testing.TB, serverPort int) (*netstack.Net, *utun) {
	t.Helper()
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, fd := range fds {
		unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_SNDBUF, 4<<20)
		unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_RCVBUF, 4<<20)
	}
	// Xray works on its own duplicate of the descriptor.
	t.Cleanup(func() { unix.Close(fds[0]) })
	t.Setenv("XRAY_TUN_FD", strconv.Itoa(fds[0]))
	cfg, err := xrayconf.Config(xrayconf.Plan{
		Node: node.Node{
			Protocol: "vless", Address: "127.0.0.1", Port: serverPort,
			UUID: testUUID, Encryption: "none",
		},
		Default: config.Proxy,
		DNS:     []string{"1.1.1.1"},
		TUN:     &xrayconf.TUN{Name: "utun99", MTU: 1500},
	})
	if err != nil {
		t.Fatal(err)
	}
	startEngine(t, string(cfg))

	dev, apps, err := netstack.CreateNetTUN([]netip.Addr{appV4, appV6}, []netip.Addr{tunDNS}, 1500)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.SetNonblock(fds[1], true); err != nil {
		t.Fatal(err)
	}
	wire := &utun{file: os.NewFile(uintptr(fds[1]), "utun"), raw: make(chan []byte, 4096)}
	t.Cleanup(func() {
		wire.file.Close()
		dev.Close()
	})
	go wire.fromApps(dev)
	go wire.toApps(dev)
	return apps, wire
}

// utun frames each packet with its address family in 4 bytes.
const utunHeader = 4

func (w *utun) fromApps(dev tun.Device) {
	buf := make([]byte, utunHeader+65535)
	bufs, sizes := [][]byte{buf}, []int{0}
	for {
		n, err := dev.Read(bufs, sizes, utunHeader)
		if err != nil {
			return
		}
		if n == 0 {
			continue
		}
		if !w.send(buf[:utunHeader+sizes[0]]) {
			return
		}
	}
}

func (w *utun) toApps(dev tun.Device) {
	buf := make([]byte, utunHeader+65535)
	for {
		n, err := w.file.Read(buf)
		if err != nil {
			return
		}
		if n <= utunHeader {
			continue
		}
		pkt := buf[utunHeader:n]
		if header.IPVersion(pkt) == header.IPv4Version && header.IPv4(pkt).DestinationAddress() == tcpip.AddrFrom4(rawV4.As4()) {
			select {
			case w.raw <- slices.Clone(pkt):
			default:
			}
			continue
		}
		dev.Write([][]byte{buf[:n]}, utunHeader)
	}
}

func (w *utun) write(t testing.TB, pkt []byte) {
	t.Helper()
	if !w.send(pkt) {
		t.Fatal("the utun stand-in is closed")
	}
}

// send fills in the family and writes pkt, waiting out a full socket
// buffer the way a utun writer would block.
func (w *utun) send(pkt []byte) bool {
	pkt[0], pkt[1], pkt[2], pkt[3] = 0, 0, 0, unix.AF_INET
	if pkt[utunHeader]>>4 == 6 {
		pkt[3] = unix.AF_INET6
	}
	for {
		_, err := w.file.Write(pkt)
		if err == nil {
			return true
		}
		if !errors.Is(err, unix.ENOBUFS) {
			return false
		}
		time.Sleep(50 * time.Microsecond)
	}
}

// tcpPacket is a bare IPv4 TCP segment with room for the utun header.
func tcpPacket(src, dst netip.AddrPort, seq, ack uint32, flags header.TCPFlags) []byte {
	pkt := make([]byte, utunHeader+header.IPv4MinimumSize+header.TCPMinimumSize)
	srcAddr, dstAddr := tcpip.AddrFrom4(src.Addr().As4()), tcpip.AddrFrom4(dst.Addr().As4())
	ip := header.IPv4(pkt[utunHeader:])
	ip.Encode(&header.IPv4Fields{
		TotalLength: header.IPv4MinimumSize + header.TCPMinimumSize,
		TTL:         64,
		Protocol:    uint8(header.TCPProtocolNumber),
		SrcAddr:     srcAddr,
		DstAddr:     dstAddr,
	})
	ip.SetChecksum(^ip.CalculateChecksum())
	seg := header.TCP(ip.Payload())
	seg.Encode(&header.TCPFields{
		SrcPort:    src.Port(),
		DstPort:    dst.Port(),
		SeqNum:     seq,
		AckNum:     ack,
		DataOffset: header.TCPMinimumSize,
		Flags:      flags,
		WindowSize: 65535,
	})
	sum := header.PseudoHeaderChecksum(header.TCPProtocolNumber, srcAddr, dstAddr, header.TCPMinimumSize)
	seg.SetChecksum(^seg.CalculateChecksum(sum))
	return pkt
}

func appClient(apps *netstack.Net) *http.Client {
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{DialContext: apps.DialContext, DisableKeepAlives: true},
	}
}

// serveUDP answers each datagram on 127.0.0.1 with reply's result and
// returns the address.
func serveUDP(t testing.TB, reply func([]byte) []byte) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	go func() {
		b := make([]byte, 65535)
		for {
			n, from, err := conn.ReadFrom(b)
			if err != nil {
				return
			}
			if out := reply(b[:n]); out != nil {
				conn.WriteTo(out, from)
			}
		}
	}()
	return conn.LocalAddr().String()
}

func echo(b []byte) []byte { return slices.Clone(b) }

// answerDNS answers A and AAAA queries for any name with the site.
func answerDNS(q []byte) []byte {
	var p dnsmessage.Parser
	h, err := p.Start(q)
	if err != nil {
		return nil
	}
	question, err := p.Question()
	if err != nil {
		return nil
	}
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: h.ID, Response: true, RecursionAvailable: true})
	b.StartQuestions()
	b.Question(question)
	b.StartAnswers()
	rh := dnsmessage.ResourceHeader{Name: question.Name, Class: dnsmessage.ClassINET, TTL: 60}
	switch question.Type {
	case dnsmessage.TypeA:
		b.AResource(rh, dnsmessage.AResource{A: siteV4.As4()})
	case dnsmessage.TypeAAAA:
		b.AAAAResource(rh, dnsmessage.AAAAResource{AAAA: siteV6.As16()})
	}
	out, err := b.Finish()
	if err != nil {
		return nil
	}
	return out
}
