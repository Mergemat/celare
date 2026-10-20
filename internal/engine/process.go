package engine

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Running Xray in its own process is the only way to be sure a TUN
// interface is released: xray-core 26.3 never closes the TUN device when
// an instance is closed, so in-process sessions leak utuns. A process's
// descriptors all close when it exits.

// Command is how to start a child that runs ServeChild with the config
// path as its last argument (the daemon uses `celare service xray`).
type Command struct {
	Argv []string
	// Env for the child; nil inherits the parent's.
	Env []string
	// WorkDir holds the config file while the child reads it.
	WorkDir string
}

const (
	readyLine  = "celare-engine: ready"
	errorLine  = "celare-engine: error: "
	startLimit = 15 * time.Second
)

// Process is one running Xray child.
type Process struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	path  string
	done  chan struct{}
	once  sync.Once
}

// StartProcess starts a child Xray with configJSON and returns once it is
// serving, or with Xray's own error if it can't start.
func StartProcess(c Command, configJSON []byte) (*Process, error) {
	if err := os.MkdirAll(c.WorkDir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(c.WorkDir, "xray-*.json")
	if err != nil {
		return nil, err
	}
	path := f.Name()
	_, werr := f.Write(configJSON)
	f.Close()
	if werr != nil {
		os.Remove(path)
		return nil, werr
	}

	cmd := exec.Command(c.Argv[0], append(append([]string{}, c.Argv[1:]...), path)...)
	cmd.Env = c.Env
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		os.Remove(path)
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		os.Remove(path)
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		os.Remove(path)
		return nil, fmt.Errorf("start the engine process: %w", err)
	}
	p := &Process{cmd: cmd, stdin: stdin, path: path, done: make(chan struct{})}
	go func() { cmd.Wait(); close(p.done) }()

	result := make(chan error, 1)
	go func() {
		scan := bufio.NewScanner(stdout)
		for scan.Scan() {
			line := scan.Text()
			switch {
			case line == readyLine:
				result <- nil
				io.Copy(io.Discard, stdout)
				return
			case strings.HasPrefix(line, errorLine):
				result <- errors.New(strings.TrimPrefix(line, errorLine))
				return
			}
		}
		result <- errors.New("the engine process exited before it was ready")
	}()
	select {
	case err := <-result:
		if err != nil {
			p.Close()
			return nil, err
		}
		return p, nil
	case <-time.After(startLimit):
		p.Close()
		return nil, fmt.Errorf("the engine process was not ready within %s", startLimit)
	}
}

// PID is the child's process id.
func (p *Process) PID() int { return p.cmd.Process.Pid }

// Close stops the child (SIGTERM, then SIGKILL after 3s) and waits for it.
func (p *Process) Close() error {
	p.once.Do(func() {
		p.stdin.Close()
		p.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-p.done:
		case <-time.After(3 * time.Second):
			p.cmd.Process.Kill()
			<-p.done
		}
		os.Remove(p.path)
	})
	return nil
}

// ServeChild is the child's side: read and delete the config, start Xray,
// say ready, and run until told to stop or the parent goes away (stdin
// closes).
func ServeChild(configPath string, stdin io.Reader, stdout io.Writer) error {
	cfg, err := os.ReadFile(configPath)
	os.Remove(configPath) // it holds credentials
	if err != nil {
		fmt.Fprintln(stdout, errorLine+err.Error())
		return err
	}
	e, err := Start(cfg)
	if err != nil {
		fmt.Fprintln(stdout, errorLine+err.Error())
		return err
	}
	fmt.Fprintln(stdout, readyLine)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	parentGone := make(chan struct{})
	go func() { io.Copy(io.Discard, stdin); close(parentGone) }()
	select {
	case <-stop:
	case <-parentGone:
	}
	return e.Close()
}
