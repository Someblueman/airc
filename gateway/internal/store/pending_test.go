//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
)

func TestHasPendingDeliveryLifecycle(t *testing.T) {
	c, identity, _ := fixture(t)
	ctx := context.Background()
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	check := func(want bool) {
		t.Helper()
		got, err := s.HasPending(ctx)
		if err != nil || got != want {
			t.Fatalf("pending %v, want %v: %v", got, want, err)
		}
	}
	check(false)
	now := time.Unix(1000, 0)
	if _, _, err := s.AdmitAuthor(ctx, identity, request(), 0, now); err != nil {
		t.Fatal(err)
	}
	check(true)
	g, attempt := consumeAttempt(t, s, scopeOf(s, identity), SendAttempt, now)
	check(true)
	if _, err := s.RecordAcceptance(ctx, g, receiptFor(attempt), now, now); err != nil {
		t.Fatal(err)
	}
	check(false)
	r := request()
	r.RequestID = "uncertain"
	if _, _, err := s.AdmitAuthor(ctx, identity, r, 0, now); err != nil {
		t.Fatal(err)
	}
	g, err = s.Reserve(ctx, scopeOf(s, identity), r.RequestID, SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Consume(now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Complete(ctx, g, Completion{Failure: core.Failure{Code: core.DeliveryUncertain, Transient: true}, MayHavePosted: true}, now); err != nil {
		t.Fatal(err)
	}
	check(true)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HasPending(ctx); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed: %v", err)
	}
}
