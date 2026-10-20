package engine_test

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mergemat/celare/internal/engine"
)

// The test binary doubles as the child: StartProcess runs it with
// CELARE_ENGINE_CHILD=1, as the daemon runs `celare service xray`.
func TestMain(m *testing.M) {
	if os.Getenv("CELARE_ENGINE_CHILD") == "1" {
		if err := engine.ServeChild(os.Args[len(os.Args)-1], os.Stdin, os.Stdout); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func childCommand(t *testing.T) engine.Command {
	t.Helper()
	return engine.Command{
		Argv:    []string{os.Args[0], "-test.run=^$"},
		Env:     append(os.Environ(), "CELARE_ENGINE_CHILD=1"),
		WorkDir: t.TempDir(),
	}
}

// Traffic flows through the child's Xray, and closing it ends the
// process, which is what releases a TUN interface.
func TestProcessCarriesTrafficAndExitsOnClose(t *testing.T) {
	direct := answer(t, "direct")
	socks := freePort(t)
	cfg := fmt.Sprintf(`{
		"inbounds": [{"listen": "127.0.0.1", "port": %d, "protocol": "socks", "settings": {"udp": true}}],
		"outbounds": [{"protocol": "freedom"}]
	}`, socks)
	p, err := engine.StartProcess(childCommand(t), []byte(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if got := get(t, socksClient(t, socks), "http://"+direct); got != "direct" {
		t.Errorf("through the child engine got %q, want direct", got)
	}
	pid := p.PID()
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if proc, err := os.FindProcess(pid); err == nil && proc.Signal(nil) == nil {
		t.Errorf("child %d still running after Close", pid)
	}
	if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", socks), 300*time.Millisecond); err == nil {
		c.Close()
		t.Errorf("SOCKS port still open after Close")
	}
}

func TestProcessReportsXraysStartError(t *testing.T) {
	start := time.Now()
	_, err := engine.StartProcess(childCommand(t), []byte(`{"outbounds": [{"protocol": "no-such-protocol"}]}`))
	if err == nil || !strings.Contains(err.Error(), "no-such-protocol") {
		t.Fatalf("err = %v, want Xray's complaint about the protocol", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("took %s to report a bad config", time.Since(start))
	}
}

// The config holds credentials; it must not outlive the process.
func TestProcessRemovesItsConfigFile(t *testing.T) {
	cmd := childCommand(t)
	p, err := engine.StartProcess(cmd, []byte(`{"outbounds": [{"protocol": "freedom"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	p.Close()
	left, _ := filepath.Glob(filepath.Join(cmd.WorkDir, "*"))
	if len(left) != 0 {
		t.Errorf("files left behind: %v", left)
	}
}
