package engine_test

import (
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Mergemat/celare/internal/engine"
)

// The test binary doubles as the child: Pool runs it with
// CELARE_ENGINE_CHILD=1, as the daemon runs `celare service xray`.
func TestMain(m *testing.M) {
	if os.Getenv("CELARE_ENGINE_CHILD") == "1" {
		if err := engine.ServeChild(os.Stdin, os.Stdout); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func childCommand(t *testing.T) engine.Command {
	t.Helper()
	return engine.Command{
		Argv: []string{os.Args[0], "-test.run=^$"},
		Env:  append(os.Environ(), "CELARE_ENGINE_CHILD=1"),
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
	p, err := startProcess(t, []byte(cfg))
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
	_, err := startProcess(t, []byte(`{"outbounds": [{"protocol": "no-such-protocol"}]}`))
	if err == nil || !strings.Contains(err.Error(), "no-such-protocol") {
		t.Fatalf("err = %v, want Xray's complaint about the protocol", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("took %s to report a bad config", time.Since(start))
	}
}

// A replacement process must also carry traffic after the previous child
// exits. Neither Xray listeners nor the process can be reused across sessions.
func TestPoolStartsFreshProcesses(t *testing.T) {
	pool := engine.NewPool(childCommand(t))
	defer pool.Close()
	direct := answer(t, "next session")
	var previous int
	for range 2 {
		socks := freePort(t)
		cfg := fmt.Sprintf(`{"inbounds":[{"listen":"127.0.0.1","port":%d,"protocol":"socks"}],"outbounds":[{"protocol":"freedom"}]}`, socks)
		p, err := pool.Start([]byte(cfg))
		if err != nil {
			t.Fatal(err)
		}
		if previous == p.PID() {
			t.Fatal("reused a process")
		}
		if got := get(t, socksClient(t, socks), "http://"+direct); got != "next session" {
			t.Fatalf("got %q", got)
		}
		previous = p.PID()
		p.Close()
	}
}

func startProcess(t *testing.T, cfg []byte) (*engine.Process, error) {
	t.Helper()
	pool := engine.NewPool(childCommand(t))
	t.Cleanup(func() { pool.Close() })
	p, err := pool.Start(cfg)
	if p != nil {
		t.Cleanup(func() { p.Close() })
	}
	return p, err
}
