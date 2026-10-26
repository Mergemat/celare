package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Mergemat/celare/internal/config"
	"github.com/Mergemat/celare/internal/system"
	"github.com/Mergemat/celare/internal/xrayconf"
	"github.com/spf13/cobra"
)

const starterConfig = `# celare configuration. Edit freely; comments are kept when celare edits it.
# Check it with:   celare config check
# Use it with:     celare apply   (the running tunnel only changes on apply)

# Where traffic goes when no rule matches: "proxy", "direct" or "block".
default = "proxy"

# DNS servers for every lookup. Queries follow the rules like other traffic,
# so by default they go through the proxy.
dns = ["https://1.1.1.1/dns-query"]

# Nodes you add by hand ('celare nodes add <link>') are stored as share links:
# [[node]]
# name = "home"
# link = "vless://..."

# Subscriptions ('celare subs add <name> <url>') list nodes from a panel;
# their nodes are named <subscription>/<name>:
# [[subscription]]
# name = "main"
# url = "https://panel.example.com/sub/..."

# Rules are checked top to bottom; the first match wins.
# See 'celare rules --help' for the match syntax.

# Local networks never go through the proxy.
[[rule]]
name = "local"
action = "direct"
ips = ["10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "127.0.0.0/8", "169.254.0.0/16", "100.64.0.0/10", "fc00::/7", "fe80::/10"]
`

func (a *app) configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Create, check and inspect config.toml",
	}
	cmd.AddCommand(a.configInitCmd(), a.configCheckCmd(), a.configPathCmd(), a.configRenderCmd())
	return cmd
}

func (a *app) configInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create config.toml with commented defaults",
		Args:  exactArgs(0, "no arguments"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := a.configPath()
			if _, err := os.Stat(path); err == nil {
				return failure("config_exists", path+" already exists", "edit it, or move it away to start over")
			}
			if err := os.MkdirAll(a.env.ConfigDir, 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(starterConfig), 0o600); err != nil {
				return err
			}
			return a.ok(map[string]string{"path": path}, func(w io.Writer) {
				fmt.Fprintln(w, "created "+path)
			}, "celare subs add <name> <url>", "celare nodes add <share-link>")
		},
	}
}

func (a *app) configCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Validate config.toml and report every problem",
		Args:  exactArgs(0, "no arguments"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			if problems := cfg.Check(); len(problems) > 0 {
				return &apiError{Code: "config_invalid", Message: "config.toml has problems", Problems: problems, Hint: "fix them and run `celare config check` again"}
			}
			warnings := []string{}
			for _, s := range cfg.Subscriptions {
				if _, ok := a.library().SubscriptionInfo(s.Name); !ok {
					warnings = append(warnings, fmt.Sprintf("subscription %q was never fetched; run `celare subs update %s`", s.Name, s.Name))
				}
			}
			if usesGeo(cfg) && !geoFilesPresent(a.env.ConfigDir) {
				warnings = append(warnings, "rules use geosite:/geoip: but geosite.dat/geoip.dat are missing; run `celare rules geo-update`")
			}
			warnings = append(warnings, a.dnsWarnings(cfg)...)
			return a.ok(map[string]any{"path": a.configPath(), "warnings": warnings}, func(w io.Writer) {
				fmt.Fprintln(w, "config.toml is valid")
				for _, wr := range warnings {
					fmt.Fprintln(w, "warning: "+wr)
				}
			}, "celare apply --dry-run")
		},
	}
}

func usesGeo(cfg config.Config) bool {
	for _, r := range cfg.Rules {
		for _, m := range append(append([]string{}, r.Domains...), r.IPs...) {
			if strings.HasPrefix(m, "geosite:") || strings.HasPrefix(m, "geoip:") {
				return true
			}
		}
	}
	return false
}

func geoFilesPresent(dir string) bool {
	for _, f := range []string{"geosite.dat", "geoip.dat"} {
		if _, err := os.Stat(filepath.Join(dir, "geo", f)); err != nil {
			return false
		}
	}
	return true
}

func (a *app) configPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print where config.toml and the subscription cache live",
		Args:  exactArgs(0, "no arguments"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			data := map[string]string{
				"dir":           a.env.ConfigDir,
				"config":        a.configPath(),
				"subscriptions": filepath.Join(a.env.ConfigDir, "subscriptions"),
			}
			return a.ok(data, func(w io.Writer) { fmt.Fprintln(w, a.configPath()) })
		},
	}
}

func (a *app) configRenderCmd() *cobra.Command {
	var reveal bool
	cmd := &cobra.Command{
		Use:   "render <node>",
		Short: "Print the Xray config celare would run for a node",
		Args:  exactArgs(1, "one node name"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			n, err := a.library().Resolve(cfg, args[0])
			if err != nil {
				return failure("node_not_found", err.Error(), "")
			}
			if !reveal {
				n = n.Redacted()
			}
			iface := "en0"
			if route, _, err := (&system.Tunnel{}).Physical(); err == nil {
				iface = route.Interface
			}
			def := cfg.Default
			if def == "" {
				def = config.Proxy
			}
			dns := cfg.DNS
			if len(dns) == 0 {
				dns = config.DefaultDNS
			}
			out, err := xrayconf.Config(xrayconf.Plan{
				Node: n, Default: def, Rules: cfg.Rules, DNS: dns,
				TUN: &xrayconf.TUN{Name: "utunN", MTU: 1500}, BindInterface: iface,
			})
			if err != nil {
				return failure("render_failed", err.Error(), "")
			}
			raw := jsonRaw(out)
			return a.ok(raw, func(w io.Writer) { w.Write(out); fmt.Fprintln(w) })
		},
	}
	cmd.Flags().BoolVar(&reveal, "reveal", false, "include credentials")
	return cmd
}

type jsonRaw []byte

func (j jsonRaw) MarshalJSON() ([]byte, error) {
	if len(j) == 0 {
		return nil, errors.New("empty")
	}
	return j, nil
}
