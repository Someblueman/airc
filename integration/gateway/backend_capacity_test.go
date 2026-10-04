package gateway_test

import (
	"context"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestBackendSaturationAndDeadlineRelease(t *testing.T) {
	proxy, peerDone, ready, _ := backendProxy(t, "", "stall", nil)
	const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	c, identity := acceptanceJournalFixture(t, proxy, token)
	c.Limits.Workers, c.Limits.Sessions, c.Limits.SessionsPerPrincipal = 1, 1, 1
	c.Limits.Watches, c.Limits.WatchesPerPrincipal = 0, 0
	b, err := backend.New(c)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s, err := store.Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	short := time.Now().Unix() + 2
	r := core.Request{Version: 1, RequestID: "held", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "never written", ExpiresAt: &short}
	if _, _, err := s.Admit(ctx, identity, r, 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	g, err := s.Reserve(ctx, identity.ID(), r.RequestID, store.SendAttempt, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	workerCtx, cancelWorker := context.WithCancel(ctx)
	defer cancelWorker()
	finished := make(chan struct{})
	outcomes := make(chan backend.Outcome, 1)
	go func() { defer close(finished); o, _ := b.Run(workerCtx, g); outcomes <- o }()
	t.Cleanup(func() {
		cancelWorker()
		select {
		case <-finished:
		case <-time.After(3 * time.Second):
			t.Error("attempt survived cleanup")
		}
	})
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("first lease never dialed")
	}
	r.RequestID, r.ExpiresAt = "backpressured", nil
	if _, _, err := s.Admit(ctx, identity, r, 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	g2, err := s.Reserve(ctx, identity.ID(), r.RequestID, store.SendAttempt, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	o, err := b.Run(ctx, g2)
	if err != nil || o.Completion.Failure.Code != core.CapacityExceeded || !o.Completion.Failure.Transient || o.Completion.MayHavePosted {
		t.Fatal("saturation allocated a lease or lost backpressure", o, err)
	}
	if _, err := s.Complete(ctx, g2, o.Completion, time.Now()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("first attempt missed deadline")
	}
	first := <-outcomes
	if first.Receipt != nil || first.Completion.MayHavePosted {
		t.Fatal("stalled registration claimed a write")
	}
	select {
	case <-peerDone:
	case <-time.After(time.Second):
		t.Fatal("deadline did not close peer")
	}
	g3, err := s.Reserve(ctx, identity.ID(), r.RequestID, store.SendAttempt, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	o, err = b.Run(canceled, g3)
	if err != nil || o.Completion.Failure.Code != core.UpstreamUnavailable || o.Completion.MayHavePosted {
		t.Fatal("expired lease retained its slot", o, err)
	}
}
