package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/pkg/irc"
)

func healthFixture(t *testing.T) (*agent, *faultListener) {
	t.Helper()
	s := server.New(server.Config{HistoryLimit: 100, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l := &faultListener{Listener: base}
	served := make(chan error, 1)
	go func() { served <- s.Serve(l) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			t.Error(err)
		}
		if err := <-served; err != nil {
			t.Error(err)
		}
	})
	c, err := irc.Dial(irc.Config{Nick: "load0000", Addr: l.Addr().String(), Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	a := &agent{index: 0, total: 1, client: c, alive: true, nick: "load0000", room: "#load", phaseName: "measurement", health: connectionHealth{Connected: true, AliveAtMeasurementStart: true}, cfg: config{Timeout: time.Second, BodyBytes: 64}}
	if err := c.Observe(a.room); err != nil {
		t.Fatal(err)
	}
	if err := a.operate(context.Background(), "post", "seed"); err != nil {
		t.Fatal(err)
	}
	return a, l
}

func TestIdleResetCountedOnceWithServerNotice(t *testing.T) {
	a, l := healthFixture(t)
	l.mu.Lock()
	var conn *faultConn
	for _, c := range l.active {
		conn = c
	}
	l.mu.Unlock()
	if _, err := conn.Write([]byte("ERROR :test slow consumer\r\n")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := a.receive(ctx, func(e irc.Event) bool { r, ok := e.(*irc.RawEvent); return ok && r.Command == "ERROR" }); err != nil {
		t.Fatal(err)
	}
	if f := l.disconnect(1); f.Closed != 1 {
		t.Fatalf("injection: %+v", f)
	}
	for a.alive {
		if err := a.wait(ctx, time.Now().Add(5*time.Millisecond)); err != nil {
			t.Fatal(err)
		}
	}
	first := a.health.ClosedAt
	_ = a.wait(ctx, time.Now().Add(time.Millisecond))
	if f := l.disconnect(1); f.Closed != 0 {
		t.Fatal("reset counted an already closed socket")
	}
	r := summarizeHealth([]connectionHealth{a.healthSnapshot()})
	if r.PeerDisconnects != 1 || r.IdleCloses != 1 || r.AliveAtEnd != 0 || r.ServerErrors != 1 || r.TimeoutCloses != 0 || *r.LossFraction != 1 || a.health.ClosedAt != first {
		t.Fatalf("health: %+v", r)
	}
	if a.health.LastServerError != "test slow consumer" {
		t.Fatal(a.health.LastServerError)
	}
}

func TestInFlightResetIsNotAClientTimeout(t *testing.T) {
	a, l := healthFixture(t)
	a.operating = true
	timer := time.AfterFunc(10*time.Millisecond, func() { l.disconnect(1) })
	defer timer.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := a.receive(ctx, func(irc.Event) bool { return false })
	if !errors.Is(err, io.EOF) {
		t.Fatalf("got %v", err)
	}
	a.failed(context.Background(), err)
	r := summarizeHealth([]connectionHealth{a.healthSnapshot()})
	if r.PeerDisconnects != 1 || r.TimeoutCloses != 0 || r.IdleCloses != 0 {
		t.Fatalf("health: %+v", r)
	}
}

func TestTimeoutCloseAndCancellationAreSeparate(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "timeout", true: "cancelled"}[cancelled], func(t *testing.T) {
			a, _ := healthFixture(t)
			a.operating = true
			ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			if cancelled {
				cancel()
				ctx, cancel = context.WithCancel(context.Background())
				cancel()
			}
			defer cancel()
			err := a.operate(ctx, "post", "expired")
			if !errors.Is(err, ctx.Err()) {
				t.Fatalf("got %v want %v", err, ctx.Err())
			}
			a.failed(context.Background(), err)
			r := summarizeHealth([]connectionHealth{a.healthSnapshot()})
			if r.PeerDisconnects != 0 || r.TransportFailures != 0 || r.TimeoutCloses+r.CancelledCloses != 1 || *r.LossFraction != 0 {
				t.Fatalf("health: %+v", r)
			}
			if cancelled && r.CancelledCloses != 1 || !cancelled && r.TimeoutCloses != 1 {
				t.Fatalf("misclassified: %+v", r)
			}
		})
	}
}
