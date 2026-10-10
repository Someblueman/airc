//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestGrantBeforeRevocationIsSingleUse(t *testing.T) {
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
	g, err := s.Reserve(ctx, identity.ID(), "request", SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reserve(ctx, identity.ID(), "request", SendAttempt, now); !errors.Is(err, ErrNotReady) {
		t.Fatal("parallel attempt granted", err)
	}
	gen, err := s.Revoke(ctx, identity.ID())
	if err != nil || gen != 2 {
		t.Fatal("revocation did not commit", gen, err)
	}
	if _, _, err := s.Admit(ctx, identity, request(), 0, now); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("revoked intake accepted", err)
	}
	copyOfGrant := *g
	a, err := g.Consume(now)
	if err != nil || a.Generation != 1 || a.Job.Result.SendAttempts != 1 || a.Deadline.Unix() != 1030 {
		t.Fatal("pre-revocation grant invalid", a, err)
	}
	if _, err := copyOfGrant.Consume(now); !errors.Is(err, ErrNotReady) {
		t.Fatal("value copy bypassed single consumption", err)
	}
	if _, err := (&Grant{}).Consume(now); !errors.Is(err, ErrNotReady) {
		t.Fatal("forged empty handle accepted")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Reserve(ctx, identity.ID(), "request", SendAttempt, now); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("restart forgot revocation", err)
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
	if _, _, err := s.Admit(ctx, identity, request(), 0, now); err != nil {
		t.Fatal(err)
	}
	g, err := s.Reserve(ctx, identity.ID(), "request", SendAttempt, now)
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
	j, err := s.Lookup(ctx, identity, "request")
	if err != nil || j.Result.State != core.RetryWait || !j.Result.DuplicateRisk || j.Result.SendAttempts != 1 {
		t.Fatal("restart lost uncertainty or budget", j, err)
	}
	if _, err := s.Reserve(ctx, identity.ID(), "request", SendAttempt, now); !errors.Is(err, ErrNotReady) {
		t.Fatal("send budget reset")
	}
	lookup, err := s.Reserve(ctx, identity.ID(), "request", LookupAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	a, err := lookup.Consume(now)
	if err != nil || a.Job.Result.LookupAttempts != 1 || a.Job.Result.SendAttempts != 1 {
		t.Fatal("probe budget conflated", err)
	}
}

func TestConcurrentRevokeAndReserve(t *testing.T) {
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
	var wg sync.WaitGroup
	var g *Grant
	var grantErr, revokeErr error
	wg.Go(func() { g, grantErr = s.Reserve(ctx, identity.ID(), "request", SendAttempt, now) })
	wg.Go(func() { _, revokeErr = s.Revoke(ctx, identity.ID()) })
	wg.Wait()
	if revokeErr != nil {
		t.Fatal(revokeErr)
	}
	if grantErr != nil && !errors.Is(grantErr, auth.ErrForbidden) {
		t.Fatal(grantErr)
	}
	if g != nil {
		a, err := g.Consume(now)
		if err != nil || a.Generation != 1 {
			t.Fatal("grant not ordered before revocation", err)
		}
	}
	if _, err := s.Reserve(ctx, identity.ID(), "request", LookupAttempt, now); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("post-revocation reservation accepted", err)
	}
}
