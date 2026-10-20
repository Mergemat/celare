package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Mergemat/celare/internal/api"
	"github.com/Mergemat/celare/internal/daemon"
	"github.com/Mergemat/celare/internal/engine"
	"github.com/Mergemat/celare/internal/probe"
	"github.com/Mergemat/celare/internal/system"
	"github.com/spf13/cobra"
)

const (
	cliLink      = "/usr/local/bin/celare"
	serviceLabel = "sh.celare.daemon"
	plistPath    = "/Library/LaunchDaemons/" + serviceLabel + ".plist"
	supportDir   = "/Library/Application Support/celare"
	logPath      = "/Library/Logs/celare/daemon.log"
)

// daemonBinary is root-owned: launchd must never run a binary the user
// could swap out.
var daemonBinary = filepath.Join(supportDir, "bin", "celare")

func (a *app) serviceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Install, remove or inspect the background daemon",
		Long: `The daemon runs as root under launchd because creating the tunnel interface
and changing routes and DNS need it. It listens on /var/run/celare.sock,
which only the user who installed it can open, so celare itself never
needs your password again after 'celare service install'.`,
	}
	cmd.AddCommand(a.serviceInstallCmd(), a.serviceUninstallCmd(), a.serviceStatusCmd(), a.serviceRunCmd(), a.serviceXrayCmd())
	return cmd
}

func requireRoot(command string) error {
	if os.Geteuid() != 0 {
		return failure("needs_root", command+" changes system files and needs root", "run `sudo "+command+"`")
	}
	return nil
}

// exitCode ends Run with a code after the output was already printed,
// e.g. by an escalated copy of this command.
type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

// escalate re-runs this command as root. In a terminal that is sudo, which
// prompts there; without one (an agent, the app) it is the macOS admin
// dialog, so a person still approves it.
func (a *app) escalate(args ...string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if a.json {
		args = append(args, "--json")
	}
	if stdinIsTerminal() {
		cmd := exec.Command("sudo", append([]string{self}, args...)...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, a.env.Stdout, a.env.Stderr
		if err := cmd.Run(); err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				return exitCode(exit.ExitCode())
			}
			return err
		}
		return exitCode(0)
	}
	line := shellQuote(self)
	for _, arg := range args {
		line += " " + shellQuote(arg)
	}
	script := `do shell script "` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(line) + `" with administrator privileges`
	out, err := exec.Command("osascript", "-e", script).Output()
	a.env.Stdout.Write(out)
	if err != nil {
		if len(out) == 0 {
			return failure("not_authorized", "the administrator password prompt was cancelled or failed", "run it again and approve the prompt")
		}
		return exitCode(exitError)
	}
	return exitCode(0)
}

func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// invokingUser is the user the daemon serves: --uid when given (the app
// installs through an admin prompt, which sets no SUDO_UID), else the
// user who ran sudo.
func invokingUser(uid int) (*user.User, error) {
	id := os.Getenv("SUDO_UID")
	if uid >= 0 {
		id = strconv.Itoa(uid)
	}
	if id == "" {
		return nil, failure("no_sudo_user", "can't tell which user to install for", "run it with sudo from your own account, or pass --uid")
	}
	return user.LookupId(id)
}

func plist(configDir, uid string) string {
	args := []string{daemonBinary, "service", "run", "--config", configDir, "--owner", uid}
	var b strings.Builder
	for _, a := range args {
		fmt.Fprintf(&b, "\t\t<string>%s</string>\n", xmlEscape(a))
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + serviceLabel + `</string>
	<key>ProgramArguments</key>
	<array>
` + b.String() + `	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>` + logPath + `</string>
	<key>StandardErrorPath</key>
	<string>` + logPath + `</string>
</dict>
</plist>
`
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func (a *app) serviceInstallCmd() *cobra.Command {
	var dryRun bool
	var uid int
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install and start the daemon (asks for your password)",
		Args:  exactArgs(0, "no arguments"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if uid < 0 && os.Geteuid() != 0 {
				uid = os.Getuid() // escalating below: install for whoever runs this
			}
			u, err := invokingUser(uid)
			if err != nil {
				return err
			}
			configDir := filepath.Join(u.HomeDir, ".config", "celare")
			p := plist(configDir, u.Uid)
			data := map[string]any{"plist": plistPath, "binary": daemonBinary, "configDir": configDir, "user": u.Username, "dryRun": dryRun}
			if dryRun {
				return a.ok(data, func(w io.Writer) {
					fmt.Fprintf(w, "would install %s and %s for %s:\n\n%s", daemonBinary, plistPath, u.Username, p)
				})
			}
			if os.Geteuid() != 0 {
				return a.escalate("service", "install", "--uid", u.Uid)
			}
			self, err := os.Executable()
			if err != nil {
				return err
			}
			if err := installFile(self, daemonBinary, 0o755); err != nil {
				return fmt.Errorf("install %s: %w", daemonBinary, err)
			}
			os.MkdirAll(filepath.Dir(logPath), 0o755)
			if err := os.WriteFile(plistPath, []byte(p), 0o644); err != nil {
				return err
			}
			exec.Command("launchctl", "bootout", "system/"+serviceLabel).Run()
			if out, err := exec.Command("launchctl", "bootstrap", "system", plistPath).CombinedOutput(); err != nil {
				return failure("launchctl_failed", strings.TrimSpace(string(out)), "see "+logPath)
			}
			if err := waitForSocket(a.env.Socket, 10*time.Second); err != nil {
				return failure("daemon_not_started", err.Error(), "see "+logPath)
			}
			if linkCLI(self) {
				data["cli"] = cliLink
			}
			return a.ok(data, func(w io.Writer) {
				fmt.Fprintf(w, "installed and started the daemon for %s\n", u.Username)
			}, "celare nodes list", "celare connect <node> --confirm 30s")
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be installed")
	cmd.Flags().IntVar(&uid, "uid", -1, "install for this user id instead of the one running sudo")
	return cmd
}

// linkCLI puts celare on the PATH for people who installed only the app:
// when this binary runs from a downloaded Celare.app (Homebrew links its
// own), /usr/local/bin/celare points at the root-owned installed copy,
// which survives moving or replacing the app.
func linkCLI(self string) bool {
	fromApp := strings.Contains(self, ".app/Contents/Resources/")
	fromBrew := strings.Contains(self, "/Cellar/") || strings.HasPrefix(self, "/opt/homebrew/")
	if !fromApp || fromBrew {
		return false
	}
	// The Homebrew cask already links celare on Apple Silicon.
	if _, err := os.Lstat("/opt/homebrew/bin/celare"); err == nil {
		return false
	}
	if target, err := os.Readlink(cliLink); err == nil && target != daemonBinary {
		return false // someone else's celare; leave it
	}
	if _, err := os.Lstat(cliLink); err == nil {
		os.Remove(cliLink)
	} else if !errors.Is(err, os.ErrNotExist) {
		return false
	}
	os.MkdirAll(filepath.Dir(cliLink), 0o755)
	return os.Symlink(daemonBinary, cliLink) == nil
}

func installFile(src, dst string, mode os.FileMode) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	if err := os.Chown(tmp, 0, 0); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func waitForSocket(path string, within time.Duration) error {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if c, err := net.Dial("unix", path); err == nil {
			c.Close()
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("the daemon did not open %s within %s", path, within)
}

func (a *app) serviceUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Stop and remove the daemon, restoring routes and DNS (asks for your password)",
		Args:  exactArgs(0, "no arguments"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if os.Geteuid() != 0 {
				return a.escalate("service", "uninstall")
			}
			// Stopping the daemon takes the tunnel down cleanly.
			exec.Command("launchctl", "bootout", "system/"+serviceLabel).Run()
			recoverErr := (&system.Tunnel{Run: system.Exec, RestorePath: filepath.Join(supportDir, "restore.json")}).Recover()
			for _, p := range []string{plistPath, daemonBinary, a.env.Socket} {
				os.Remove(p)
			}
			if target, err := os.Readlink(cliLink); err == nil && target == daemonBinary {
				os.Remove(cliLink)
			}
			if recoverErr != nil {
				return failure("restore_failed", recoverErr.Error(), "check DNS in System Settings → Network")
			}
			return a.ok(map[string]any{"removed": []string{plistPath, daemonBinary}}, func(w io.Writer) {
				fmt.Fprintln(w, "removed the daemon; config.toml was kept")
			})
		},
	}
}

type serviceStatus struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	PID       int    `json:"pid,omitempty"`
	Reachable bool   `json:"reachable"`
	Log       string `json:"log"`
}

func (a *app) serviceStatus(ctx context.Context) serviceStatus {
	st := serviceStatus{Log: logPath}
	if _, err := os.Stat(plistPath); err == nil {
		st.Installed = true
	}
	if out, err := exec.Command("launchctl", "print", "system/"+serviceLabel).Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line == "state = running" {
				st.Running = true
			}
			if pid, ok := strings.CutPrefix(line, "pid = "); ok {
				st.PID, _ = strconv.Atoi(pid)
			}
		}
	}
	if _, err := a.client().Tunnel(ctx); err == nil {
		st.Reachable = true
	}
	return st
}

func (a *app) serviceStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether the daemon is installed, running and reachable",
		Args:  exactArgs(0, "no arguments"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			st := a.serviceStatus(cmd.Context())
			var next []string
			if !st.Installed {
				next = []string{"celare service install"}
			}
			return a.ok(st, func(w io.Writer) {
				fmt.Fprintf(w, "installed: %v  running: %v  reachable: %v\nlog: %s\n", st.Installed, st.Running, st.Reachable, st.Log)
			}, next...)
		},
	}
}

func (a *app) serviceRunCmd() *cobra.Command {
	var configDir string
	var owner int
	cmd := &cobra.Command{
		Use:    "run",
		Short:  "Run the daemon in the foreground (launchd does this)",
		Hidden: true,
		Args:   exactArgs(0, "no arguments"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireRoot("celare service run"); err != nil {
				return err
			}
			return runDaemon(cmd.Context(), configDir, owner, a.env.Socket)
		},
	}
	cmd.Flags().StringVar(&configDir, "config", "", "the user's config directory")
	cmd.Flags().IntVar(&owner, "owner", -1, "uid allowed to use the socket")
	cmd.MarkFlagRequired("config")
	cmd.MarkFlagRequired("owner")
	return cmd
}

// serviceXrayCmd is the child process the daemon runs each tunnel's Xray
// in, so stopping a tunnel ends a process and releases its utun.
func (a *app) serviceXrayCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "xray <config>",
		Short:  "Run one tunnel's Xray (the daemon starts this)",
		Hidden: true,
		Args:   exactArgs(1, "a config file"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := engine.ServeChild(args[0], os.Stdin, os.Stdout); err != nil {
				return exitCode(exitError)
			}
			return exitCode(exitOK)
		},
	}
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }
func (wallClock) AfterFunc(d time.Duration, f func()) func() bool {
	return time.AfterFunc(d, f).Stop
}

// systemProbe checks the new tunnel the way any app would use it, and
// says which stage failed.
type systemProbe struct{}

func (systemProbe) Probe(ctx context.Context, url string) error { return probe.System(ctx, url) }

// diagnose runs after the tunnel's traffic check failed and the previous
// state is back: it tries the node on its own over the same upstream the
// tunnel used, to tell a bad node from a tunnel fault.
func diagnose(t *system.Tunnel) func(context.Context, *daemon.Spec) string {
	return func(ctx context.Context, spec *daemon.Spec) string {
		up, err := t.Upstream("")
		if err != nil {
			return err.Error()
		}
		r := probe.Node(ctx, spec.Node, probe.DefaultURL, 6*time.Second, up.Interface)
		if r.OK {
			return fmt.Sprintf("%s works on its own over %s (%d ms), so traffic broke inside celare's tunnel; /Library/Logs/celare/xray.log has the details", spec.NodeName, up.Interface, r.LatencyMs)
		}
		return fmt.Sprintf("%s doesn't work over %s on its own either (%s); try another node, `celare nodes test` checks them all", spec.NodeName, up.Interface, r.Error)
	}
}

func runDaemon(ctx context.Context, configDir string, owner int, socket string) error {
	log.SetFlags(log.LstdFlags)
	os.MkdirAll(supportDir, 0o755)
	// geoip:/geosite: rules read their lists from here.
	os.Setenv("XRAY_LOCATION_ASSET", filepath.Join(configDir, "geo"))

	self, err := os.Executable()
	if err != nil {
		return err
	}
	xrayCmd := engine.Command{Argv: []string{self, "service", "xray"}, WorkDir: filepath.Join(supportDir, "run")}
	tunnel := &system.Tunnel{
		Run: system.Exec,
		StartEngine: func(cfg []byte) (io.Closer, error) {
			return engine.StartProcess(xrayCmd, cfg)
		},
		XrayLog:     filepath.Join(filepath.Dir(logPath), "xray.log"),
		Lookup:      func(host string) ([]net.IP, error) { return net.LookupIP(host) },
		CanReach:    system.DialBound,
		RestorePath: filepath.Join(supportDir, "restore.json"),
		Routes:      system.DefaultRoutes,
		Interfaces:  system.InterfaceNames,
		Logf:        log.Printf,
	}
	if err := tunnel.Recover(); err != nil {
		log.Printf("recovering a crashed session: %v", err)
	}
	d := daemon.New(tunnel, wallClock{}, systemProbe{})
	srv := &api.Server{
		Diagnose:  diagnose(tunnel),
		Version:   Version,
		Daemon:    d,
		ConfigDir: configDir,
		StatePath: filepath.Join(supportDir, "state.json"),
		System: func() *api.SystemInfo {
			return &api.SystemInfo{Interface: tunnel.Interface(), Tun: tunnel.TunName()}
		},
	}
	if err := srv.Resume(ctx); err != nil {
		log.Printf("resuming the last tunnel: %v", err)
	}
	stop, err := srv.Listen(socket, owner)
	if err != nil {
		return err
	}
	log.Printf("listening on %s for uid %d, config in %s", socket, owner, configDir)

	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	watchNetwork(ctx, d, tunnel)
	stop()
	// Restore routes and DNS but keep state.json: after a reboot the
	// tunnel comes back.
	if err := tunnel.Stop(); err != nil {
		log.Printf("stopping the tunnel: %v", err)
	}
	return nil
}

// watchNetwork restarts the tunnel when its upstream changes (Wi-Fi to
// Ethernet, another VPN connecting or disconnecting), since Xray's
// outbounds are bound to it.
func watchNetwork(ctx context.Context, d *daemon.Daemon, t *system.Tunnel) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		bound := t.Interface()
		if bound == "" {
			continue
		}
		up, err := t.Upstream(t.TunName())
		if err != nil || up.Interface == bound {
			continue
		}
		if err := d.Restart(fmt.Sprintf("upstream moved from %s to %s", bound, up.Interface)); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("restart after network change: %v", err)
		}
	}
}
