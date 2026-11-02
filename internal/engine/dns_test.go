package engine_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/Mergemat/celare/internal/config"
	"github.com/Mergemat/celare/internal/node"
	"github.com/Mergemat/celare/internal/xrayconf"
	"golang.org/x/net/dns/dnsmessage"
)

// A rule's dns server answers for the rule's domains only, and its
// queries go the rule's way even when a later rule names its address.
func TestRuleDNSServerResolvesItsDomains(t *testing.T) {
	ruleDNS := fakeDNS(t, "192.0.2.1")
	listDNS := fakeDNS(t, "192.0.2.2")
	_, rulePort, _ := net.SplitHostPort(ruleDNS)
	cfg, err := xrayconf.Config(xrayconf.Plan{
		// The node is never used: nothing here goes through the proxy.
		Node:    node.Node{Protocol: "trojan", Address: "203.0.113.1", Port: 443, Password: "pw"},
		Default: config.Direct,
		Rules: []config.Rule{
			{Action: config.Direct, Domains: []string{"domain:example.cn"}, DNS: "tcp://" + ruleDNS},
			{Action: config.Block, IPs: []string{"127.0.0.0/8"}, Port: rulePort},
		},
		DNS: []string{"tcp://" + listDNS},
	})
	if err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	startEngine(t, string(withDNSInbound(t, cfg, port)))

	r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "udp", "127.0.0.1:"+strconv.Itoa(port))
	}}
	for host, want := range map[string]string{"www.example.cn": "192.0.2.1", "www.example.com": "192.0.2.2"} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		ips, err := r.LookupIP(ctx, "ip4", host)
		cancel()
		if err != nil || len(ips) != 1 || ips[0].String() != want {
			t.Errorf("%s resolved to %v (%v), want %s", host, ips, err, want)
		}
	}
}

// withDNSInbound adds a UDP port whose queries Xray's DNS answers, as it
// answers port 53 in the TUN.
func withDNSInbound(t *testing.T, cfg []byte, port int) []byte {
	t.Helper()
	var c map[string]any
	if err := json.Unmarshal(cfg, &c); err != nil {
		t.Fatal(err)
	}
	c["inbounds"] = []any{map[string]any{
		"tag": "dns-in", "protocol": "dokodemo-door", "listen": "127.0.0.1", "port": port,
		"settings": map[string]any{"address": "192.0.2.53", "port": 53, "network": "udp"},
	}}
	routing := c["routing"].(map[string]any)
	routing["rules"] = append([]any{map[string]any{"inboundTag": []string{"dns-in"}, "outboundTag": "dns-out"}}, routing["rules"].([]any)...)
	out, _ := json.Marshal(c)
	return out
}

// fakeDNS serves DNS over TCP on 127.0.0.1, answering every A query with
// ip, and returns its host:port.
func fakeDNS(t *testing.T, ip string) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	a := [4]byte(net.ParseIP(ip).To4())
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				for {
					var size uint16
					if binary.Read(conn, binary.BigEndian, &size) != nil {
						return
					}
					req := make([]byte, size)
					if _, err := io.ReadFull(conn, req); err != nil {
						return
					}
					var p dnsmessage.Parser
					h, err := p.Start(req)
					if err != nil {
						return
					}
					q, err := p.Question()
					if err != nil {
						return
					}
					b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: h.ID, Response: true, Authoritative: true})
					b.StartQuestions()
					b.Question(q)
					b.StartAnswers()
					if q.Type == dnsmessage.TypeA {
						b.AResource(dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: 60}, dnsmessage.AResource{A: a})
					}
					resp, _ := b.Finish()
					binary.Write(conn, binary.BigEndian, uint16(len(resp)))
					conn.Write(resp)
				}
			}()
		}
	}()
	return l.Addr().String()
}
