package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Mergemat/celare/internal/netconf"
	"github.com/Mergemat/celare/internal/system"
	"github.com/spf13/cobra"
)

type check struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok, warn, fail
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

func (a *app) doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check everything celare depends on and say how to fix what's wrong",
		Args:  exactArgs(0, "no arguments"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			var checks []check
			add := func(name, status, detail, fix string) {
				checks = append(checks, check{Name: name, Status: status, Detail: detail, Fix: fix})
			}

			cfg, err := a.loadConfig()
			switch {
			case err != nil:
				detail := err.Error()
				var e *apiError
				if errors.As(err, &e) {
					detail = e.Message
				}
				add("config", "fail", detail, "run `celare config init` or fix config.toml")
			case len(cfg.Check()) > 0:
				add("config", "fail", fmt.Sprintf("%d problems", len(cfg.Check())), "run `celare config check`")
			default:
				add("config", "ok", a.configPath(), "")
				for _, s := range cfg.Subscriptions {
					info, ok := a.library().SubscriptionInfo(s.Name)
					switch {
					case !ok:
						add("subscription "+s.Name, "warn", "never fetched", "run `celare subs update "+s.Name+"`")
					case time.Since(info.UpdatedAt) > 7*24*time.Hour:
						add("subscription "+s.Name, "warn", "last fetched "+info.UpdatedAt.Local().Format("2006-01-02"), "run `celare subs update "+s.Name+"`")
					default:
						add("subscription "+s.Name, "ok", fmt.Sprintf("%d nodes", info.Nodes), "")
					}
				}
				if usesGeo(cfg) && !geoFilesPresent(a.env.ConfigDir) {
					add("geo lists", "fail", "rules use geosite:/geoip: but the lists are missing", "run `celare rules geo-update`")
				}
			}

			svc := a.serviceStatus(cmd.Context())
			tunnel, tunnelErr := a.client().Tunnel(cmd.Context())
			switch {
			case !svc.Installed:
				add("daemon", "fail", "not installed", "run `celare service install`")
			case tunnelErr != nil:
				add("daemon", "fail", "installed but not answering on "+a.env.Socket, "see "+svc.Log+", then `celare service install` to restart it")
			case tunnel.Version != Version:
				add("daemon", "warn", fmt.Sprintf("daemon is %s, this CLI is %s", tunnel.Version, Version), "run `celare service install` to update it")
			default:
				add("daemon", "ok", fmt.Sprintf("%s, running (pid %d)", tunnel.Version, svc.PID), "")
			}
			if tunnelErr == nil && tunnel.State == "up" {
				ip, latency, err := exitIP(cmd.Context())
				if err != nil {
					add("tunnel", "fail", fmt.Sprintf("up on %s but traffic doesn't get through: %v", tunnel.Node, err), "run `celare nodes test` and connect to a node that works")
				} else {
					add("tunnel", "ok", fmt.Sprintf("%s carries traffic: exit IP %s, %d ms", tunnel.Node, ip, latency.Milliseconds()), "")
				}
			}

			t := &system.Tunnel{Run: system.Exec}
			route, service, err := t.Physical()
			if err != nil {
				add("network", "fail", err.Error(), "connect Wi-Fi or Ethernet")
			} else {
				add("network", "ok", fmt.Sprintf("%s via %s (%s)", route.Interface, route.Gateway, service), "")
				// celare reaches its servers by leaving through the physical
				// interface directly; some VPN firewalls refuse exactly that.
				if err := system.DialBound("1.1.1.1", 443, route.Interface); err != nil {
					add("direct path", "fail", fmt.Sprintf("traffic can't leave by %s directly: %v", route.Interface, err),
						"quit VPN apps whose firewall blocks traffic outside their tunnel (Mullvad, V2Box), even when they look disconnected")
				} else {
					add("direct path", "ok", "servers are reachable over "+route.Interface, "")
				}
				if tunnelErr == nil && tunnel.State == "down" {
					if out, err := system.Exec("networksetup", "-getdnsservers", service); err == nil &&
						strings.Contains(out, netconf.TunnelDNS) {
						add("dns", "fail", service+" still points DNS at the tunnel, which is down", "run `networksetup -setdnsservers \""+service+"\" Empty`")
					}
				}
			}
			ours := ""
			if tunnelErr == nil && tunnel.System != nil {
				ours = tunnel.System.Tun
			}
			if out, err := system.Exec("netstat", "-rn", "-f", "inet"); err == nil {
				if foreign := netconf.ForeignTunnels(netconf.ParseDefaultRoutes(out), ours); len(foreign) > 0 {
					add("other VPNs", "warn", "default route held by "+strings.Join(foreign, ", "),
						"turn off other VPN apps (V2Box, a Tailscale exit node, ...) while celare is connected")
				} else {
					add("other VPNs", "ok", "none", "")
				}
			}

			failed := 0
			for _, c := range checks {
				if c.Status == "fail" {
					failed++
				}
			}
			var next []string
			for _, c := range checks {
				if c.Fix != "" && strings.HasPrefix(c.Fix, "run `") {
					next = append(next, strings.TrimSuffix(strings.TrimPrefix(c.Fix, "run `"), "`"))
				}
			}
			print := func(w io.Writer) {
				for _, c := range checks {
					mark := map[string]string{"ok": "✓", "warn": "!", "fail": "✗"}[c.Status]
					fmt.Fprintf(w, "%s %-18s %s\n", mark, c.Name, c.Detail)
					if c.Fix != "" {
						fmt.Fprintf(w, "  fix: %s\n", c.Fix)
					}
				}
			}
			if failed > 0 {
				if !a.json {
					print(a.env.Stdout)
				}
				return &apiError{Code: "doctor_failed", Message: fmt.Sprintf("%d of %d checks failed", failed, len(checks)), Hint: "apply the fixes listed with each check"}
			}
			return a.ok(checks, print, next...)
		},
	}
}

// exitIP fetches the public IP the internet sees, which through a working
// tunnel is the node's.
func exitIP(ctx context.Context) (string, time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.ipify.org", nil)
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	return strings.TrimSpace(string(b)), time.Since(start), err
}
