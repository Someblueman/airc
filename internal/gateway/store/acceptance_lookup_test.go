//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestRetainedLookupResolvesAcceptanceAfterRestart(t *testing.T) {
	c, identity, _ := fixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Admit(ctx, identity, request(), 0, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reserve(ctx, scopeOf(s, identity), "request", LookupAttempt, now); !errors.Is(err, ErrNotReady) {
		t.Fatal("lookup allowed before any send", err)
	}
	g, err := s.Reserve(ctx, scopeOf(s, identity), "request", SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Consume(now); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	g, err = s.Reserve(ctx, scopeOf(s, identity), "request", LookupAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	a, err := g.Consume(now)
	if err != nil {
		t.Fatal(err)
	}
	j, err := s.RecordAcceptance(ctx, g, receiptFor(a), now, now)
	if err != nil || j.Result.State != core.Accepted || j.Result.SendAttempts != 1 || j.Result.LookupAttempts != 1 {
		t.Fatal("retained lookup did not resolve prior send", j, err)
	}
	if _, err := s.Reserve(ctx, scopeOf(s, identity), "request", SendAttempt, now); !errors.Is(err, ErrNotReady) {
		t.Fatal("lookup acceptance allowed resend", err)
	}
}

func TestAcceptanceFencesAttemptKind(t *testing.T) {
	c, identity, _ := fixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err := s.Admit(ctx, identity, request(), 0, now); err != nil {
		t.Fatal(err)
	}
	g, err := s.Reserve(ctx, scopeOf(s, identity), "request", SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	a, err := g.Consume(now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE jobs SET attempt_kind='lookup'"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordAcceptance(ctx, g, receiptFor(a), now, now); !errors.Is(err, ErrStaleAttempt) {
		t.Fatal("completion ignored changed attempt kind", err)
	}
	j, err := s.Lookup(ctx, identity, "request")
	if err != nil || j.Result.State == core.Accepted || j.Receipt != nil {
		t.Fatal("stale completion persisted acceptance", j, err)
	}
}
