package engine

import (
	"bufio"
	"bytes"
	"encoding/json"
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

// Each tunnel gets its own process because Xray does not release its TUN
// descriptor on Close. Preparing the next process ahead of time keeps process
// launch and library initialization out of the connect path.
type Command struct {
	Argv []string
	Env  []string
}

const (
	waitingLine = "celare-engine: waiting"
	readyLine   = "celare-engine: ready"
	errorLine   = "celare-engine: error: "
	startLimit  = 15 * time.Second
)

type Process struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	done   chan struct{}
	status chan error
	once   sync.Once
}

func prepare(c Command) (*Process, error) {
	cmd := exec.Command(c.Argv[0], c.Argv[1:]...)
	cmd.Env, cmd.Stderr = c.Env, os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		return nil, fmt.Errorf("start the engine process: %w", err)
	}
	p := &Process{cmd: cmd, stdin: stdin, done: make(chan struct{}), status: make(chan error, 2)}
	go func() {
		defer close(p.status)
		scan := bufio.NewScanner(stdout)
		for scan.Scan() {
			switch line := scan.Text(); {
			case line == waitingLine, line == readyLine:
				p.status <- nil
			case strings.HasPrefix(line, errorLine):
				p.status <- errors.New(strings.TrimPrefix(line, errorLine))
			}
		}
		cmd.Wait()
		close(p.done)
	}()
	if err := p.wait(); err != nil {
		p.Close()
		return nil, err
	}
	return p, nil
}

func (p *Process) wait() error {
	timer := time.NewTimer(startLimit)
	defer timer.Stop()
	select {
	case err, ok := <-p.status:
		if !ok {
			return errors.New("the engine process exited before it was ready")
		}
		return err
	case <-timer.C:
		return fmt.Errorf("the engine process was not ready within %s", startLimit)
	}
}

func (p *Process) start(configJSON []byte) (*Process, error) {
	// One JSON value on stdin replaces the credential-bearing temporary file.
	var compact bytes.Buffer
	if err := json.Compact(&compact, configJSON); err != nil {
		p.Close()
		return nil, err
	}
	compact.WriteByte('\n')
	if _, err := p.stdin.Write(compact.Bytes()); err != nil {
		p.Close()
		return nil, err
	}
	if err := p.wait(); err != nil {
		p.Close()
		return nil, err
	}
	return p, nil
}

func (p *Process) PID() int { return p.cmd.Process.Pid }

// Close releases the process's sockets immediately. There is no persistent
// state inside Xray to flush, and the daemon has already restored DNS/routes.
func (p *Process) Close() error {
	p.once.Do(func() {
		p.stdin.Close()
		p.cmd.Process.Kill()
		<-p.done
	})
	return nil
}

// ServeChild waits for a config without opening a tunnel, then serves until
// signalled or its parent closes stdin. The process owns no on-disk secrets.
func ServeChild(stdin io.Reader, stdout io.Writer) error {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(stop)
	fmt.Fprintln(stdout, waitingLine)
	reader := bufio.NewReader(stdin)
	cfg, err := reader.ReadBytes('\n')
	if err != nil {
		return err
	}
	e, err := Start(cfg)
	if err != nil {
		fmt.Fprintln(stdout, errorLine+err.Error())
		return err
	}
	fmt.Fprintln(stdout, readyLine)
	parentGone := make(chan struct{})
	go func() { io.Copy(io.Discard, reader); close(parentGone) }()
	select {
	case <-stop:
	case <-parentGone:
	}
	return e.Close()
}
