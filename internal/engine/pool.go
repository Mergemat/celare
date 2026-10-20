package engine

import (
	"errors"
	"sync"
)

// Pool keeps one unused child ready. Start consumes it and prepares its
// replacement in the background. A used child is never reused for a tunnel.
// Close releases unused children; callers close the active Process themselves.
type Pool struct {
	command Command
	next    chan prepared
	done    chan struct{}
	once    sync.Once
	wg      sync.WaitGroup
	mu      sync.Mutex
	closed  bool
}

type prepared struct {
	process *Process
	err     error
}

func NewPool(command Command) *Pool {
	p := &Pool{command: command, next: make(chan prepared, 1), done: make(chan struct{})}
	p.refill()
	return p
}

func (p *Pool) refill() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		child, err := prepare(p.command)
		select {
		case p.next <- prepared{child, err}:
		case <-p.done:
			if child != nil {
				child.Close()
			}
		}
	}()
}

func (p *Pool) Start(configJSON []byte) (*Process, error) {
	select {
	case <-p.done:
		return nil, errors.New("engine pool is closed")
	case next := <-p.next:
		p.mu.Lock()
		closed := p.closed
		p.mu.Unlock()
		if closed {
			if next.process != nil {
				next.process.Close()
			}
			return nil, errors.New("engine pool is closed")
		}
		p.refill()
		if next.err != nil {
			return nil, next.err
		}
		return next.process.start(configJSON)
	}
}

func (p *Pool) Close() error {
	p.once.Do(func() {
		p.mu.Lock()
		p.closed = true
		close(p.done)
		p.mu.Unlock()
		p.wg.Wait()
		select {
		case next := <-p.next:
			if next.process != nil {
				next.process.Close()
			}
		default:
		}
	})
	return nil
}
