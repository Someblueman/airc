//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
)

func TestGrantIsSingleUse(t *testing.T) {
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
	if _, err := s.Reserve(ctx, scopeOf(s, identity), "request", SendAttempt, now); !errors.Is(err, ErrNotReady) {
		t.Fatal("parallel attempt granted", err)
	}
	copyOfGrant := *g
	a, err := g.Consume(now)
	if err != nil || a.Job.Result.SendAttempts != 1 || a.Deadline.Unix() != 1030 {
		t.Fatal("grant invalid", a, err)
	}
	if _, err := copyOfGrant.Consume(now); !errors.Is(err, ErrNotReady) {
		t.Fatal("value copy bypassed single consumption", err)
	}
	if _, err := (&Grant{}).Consume(now); !errors.Is(err, ErrNotReady) {
		t.Fatal("forged empty handle accepted")
	}
}

func TestRestartInvalidatesGrantAndPreservesCounters(t *testing.T) {
	c, identity, _ := fixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	c.Retry.SendAttempts = 1
	c.Retry.LookupAttempts = 1
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AdmitAuthor(ctx, identity, request(), 0, now); err != nil {
		t.Fatal(err)
	}
	g, err := s.Reserve(ctx, scopeOf(s, identity), "request", SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Consume(now); !errors.Is(err, ErrClosed) {
		t.Fatal("closed grant usable")
	}
	s, err = Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	j, err := s.LookupAuthor(ctx, identity, "request")
	if err != nil || j.Result.State != core.RetryWait || !j.Result.DuplicateRisk || j.Result.SendAttempts != 1 {
		t.Fatal("restart lost uncertainty or budget", j, err)
	}
	if _, err := s.Reserve(ctx, scopeOf(s, identity), "request", SendAttempt, now); !errors.Is(err, ErrNotReady) {
		t.Fatal("send budget reset")
	}
	lookup, err := s.Reserve(ctx, scopeOf(s, identity), "request", LookupAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	a, err := lookup.Consume(now)
	if err != nil || a.Job.Result.LookupAttempts != 1 || a.Job.Result.SendAttempts != 1 {
		t.Fatal("probe budget conflated", err)
	}
}
