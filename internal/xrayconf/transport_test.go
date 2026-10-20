package xrayconf_test

import (
	"encoding/json"
	"testing"

	"github.com/Mergemat/celare/internal/node"
	"github.com/Mergemat/celare/internal/xrayconf"
	"github.com/google/go-cmp/cmp"
)

func TestOutboundTransports(t *testing.T) {
	cases := []struct {
		name      string
		transport node.Transport
		finalmask string
		want      string
	}{
		{
			name:      "grpc multi",
			transport: node.Transport{Type: "grpc", ServiceName: "svc", Authority: "grpc.example.test", Mode: "multi"},
			want:      `{"network": "grpc", "grpcSettings": {"serviceName": "svc", "authority": "grpc.example.test", "multiMode": true}}`,
		},
		{
			name:      "httpupgrade",
			transport: node.Transport{Type: "httpupgrade", Path: "/up", Host: "up.example.test"},
			want:      `{"network": "httpupgrade", "httpupgradeSettings": {"path": "/up", "host": "up.example.test"}}`,
		},
		{
			name:      "mkcp with finalmask",
			transport: node.Transport{Type: "kcp", HeaderType: "none", MTU: 1350, TTI: 20},
			finalmask: `{"udp":[{"type":"salamander","settings":{"password":"mask-pw"}}]}`,
			want: `{
				"network": "kcp",
				"kcpSettings": {"mtu": 1350, "tti": 20},
				"finalmask": {"udp": [{"type": "salamander", "settings": {"password": "mask-pw"}}]}
			}`,
		},
		{
			name:      "raw with http header",
			transport: node.Transport{Type: "tcp", HeaderType: "http", Host: "obfs.example.test", Path: "/a,/b"},
			want: `{
				"network": "raw",
				"rawSettings": {"header": {"type": "http", "request": {
					"path": ["/a", "/b"],
					"headers": {"Host": ["obfs.example.test"]}
				}}}
			}`,
		},
		{
			name:      "raw http header defaults path to /",
			transport: node.Transport{Type: "tcp", HeaderType: "http", Host: "obfs.example.test"},
			want: `{
				"network": "raw",
				"rawSettings": {"header": {"type": "http", "request": {
					"path": ["/"],
					"headers": {"Host": ["obfs.example.test"]}
				}}}
			}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := node.Node{
				Protocol:  "vless",
				Address:   "example.test",
				Port:      443,
				UUID:      "22222222-3333-4444-9555-666666666666",
				Transport: tc.transport,
				Finalmask: tc.finalmask,
			}
			out, err := xrayconf.Outbound(n, "proxy")
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(out)
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				StreamSettings json.RawMessage `json:"streamSettings"`
			}
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(decode(t, []byte(tc.want)), decode(t, got.StreamSettings)); diff != "" {
				t.Errorf("streamSettings mismatch (-want +got):\n%s", diff)
			}
			assertXrayAccepts(t, raw)
		})
	}
}
