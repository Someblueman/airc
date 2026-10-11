//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestCompletionRequiresOwnedConsumedMatchingGrant(t *testing.T) {
	c, identity, _ := fixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err := s.AdmitAuthor(ctx, identity, request(), 0, now); err != nil {
		t.Fatal(err)
	}
	g, err := s.Reserve(ctx, scopeOf(s, identity), "request", SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	f := Completion{Failure: core.Failure{Code: core.UpstreamUnavailable, Transient: true}}
	if _, err := s.Complete(ctx, g, f, now); !errors.Is(err, ErrNotReady) {
		t.Fatal("unconsumed grant completed", err)
	}
	if _, err := g.Consume(now); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []Completion{
		{},
		{Failure: core.Failure{Code: core.InvalidRequest, Transient: true}},
		{Failure: core.Failure{Code: core.Forbidden}, MayHavePosted: true},
		{Failure: core.Failure{Code: core.UpstreamUnavailable, Transient: true, UpstreamCode: 1000}},
		{Failure: f.Failure, RetryAfterSeconds: -1},
	} {
		if _, err := s.Complete(ctx, g, invalid, now); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid classification stored", err)
		}
	}
	if _, err := s.Complete(ctx, g, f, now.Add(-time.Second)); !errors.Is(err, ErrInvalid) {
		t.Fatal("completion predates consumption", err)
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE jobs SET attempt_kind='lookup'"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Complete(ctx, g, f, now); !errors.Is(err, ErrStaleAttempt) {
		t.Fatal("mismatched kind completed", err)
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE jobs SET attempt_kind='send',attempt_id='newer'"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Complete(ctx, g, f, now); !errors.Is(err, ErrStaleAttempt) {
		t.Fatal("stale marker completed", err)
	}
}
