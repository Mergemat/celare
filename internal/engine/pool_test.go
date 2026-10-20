package engine

import (
	"os"
	"testing"
	"time"
)

func preparedCommand() Command {
	return Command{Argv: []string{os.Args[0], "-test.run=^$"}, Env: append(os.Environ(), "CELARE_ENGINE_CHILD=1")}
}

func TestPoolCloseReapsIdleChild(t *testing.T) {
	pool := NewPool(preparedCommand())
	idle := <-pool.next
	if idle.err != nil {
		pool.Close()
		t.Fatal(idle.err)
	}
	pool.next <- idle
	pool.Close()
	select {
	case <-idle.process.done:
	default:
		t.Fatal("pool left an idle process running")
	}
	if p, err := pool.Start([]byte(`{"outbounds":[{"protocol":"freedom"}]}`)); err == nil {
		p.Close()
		t.Fatal("closed pool accepted a tunnel")
	}
}

func BenchmarkPreparedProcess(b *testing.B) {
	var startTime, stopTime time.Duration
	for b.Loop() {
		b.StopTimer()
		p, err := prepare(preparedCommand())
		if err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		start := time.Now()
		if _, err := p.start([]byte(`{"outbounds":[{"protocol":"freedom"}]}`)); err != nil {
			b.Fatal(err)
		}
		startTime += time.Since(start)
		start = time.Now()
		p.Close()
		stopTime += time.Since(start)
	}
	b.ReportMetric(float64(startTime.Nanoseconds())/float64(b.N), "start-ns/op")
	b.ReportMetric(float64(stopTime.Nanoseconds())/float64(b.N), "stop-ns/op")
}
