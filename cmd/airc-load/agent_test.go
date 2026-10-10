package main

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/atomicfile"
	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/internal/testserver"
)

func TestAgentsExerciseRealChatAndCancel(t *testing.T) {
	address := testserver.Start(t, server.Config{MaxConnections: 4, HistoryLimit: 10000, Sync: atomicfile.SyncNone}, func(s *server.Server) error {
		return s.RestoreHistory(filepath.Join(t.TempDir(), "history"))
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := make(chan agentResult, 4)
	commands := make([]chan phase, 4)
	var wg sync.WaitGroup
	c := config{Rooms: 2, Rate: 50, Timeout: time.Second, BodyBytes: 64, Persist: true}
	for i := range commands {
		commands[i] = make(chan phase, 1)
		wg.Add(1)
		go func(i int) { defer wg.Done(); runAgent(ctx, c, address, i, 4, commands[i], results) }(i)
	}
	defer wg.Wait()
	defer cancel()
	for range commands {
		select {
		case r := <-results:
			if !r.ready {
				t.Fatalf("setup failed %+v", r.samples)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("setup hung")
		}
	}
	p := phase{start: time.Now().Add(50 * time.Millisecond), duration: 200 * time.Millisecond, measured: true}
	for _, ch := range commands {
		ch <- p
	}
	var samples []sample
	var mentions uint64
	for range commands {
		select {
		case r := <-results:
			samples = append(samples, r.samples...)
			if !r.health.AliveAtEnd || r.health.CloseReason != "" {
				t.Fatalf("healthy client reported loss: %+v", r.health)
			}
			mentions += r.mentions
		case <-time.After(5 * time.Second):
			t.Fatal("measurement hung")
		}
	}
	if len(samples) != 40 {
		t.Fatalf("got %d samples", len(samples))
	}
	metrics := summarize(samples)
	if len(metrics) != 6 {
		t.Fatalf("missing operation: %+v", metrics)
	}
	for op, m := range metrics {
		if m.Succeeded != m.Scheduled {
			t.Fatalf("%s: %+v", op, m)
		}
	}
	if mentions != 8 {
		t.Fatalf("mention fan-out: got %d want 8", mentions)
	}
	// Cancel with active clients and verify owned goroutines finish promptly.
	p.start = time.Now()
	p.duration = time.Minute
	for _, ch := range commands {
		ch <- p
	}
	cancel()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("agent cancellation hung")
	}
}
