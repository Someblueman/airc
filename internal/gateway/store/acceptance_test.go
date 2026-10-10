//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"github.com/Someblueman/airc/internal/gateway/core"
	"testing"
	"time"
)

func TestAcceptanceSurvivesReopenAndNeverResends(t *testing.T) {
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
	g, err := s.Reserve(ctx, identity.ID(), "request", SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	a, err := g.Consume(now)
	if err != nil {
		t.Fatal(err)
	}
	r := receiptFor(a)
	j, err := s.RecordAcceptance(ctx, g, r, now, now)
	if err != nil || j.Result.State != core.Accepted || j.Result.Accepted == nil || !*j.Result.Accepted || j.Result.Persisted == nil || *j.Result.Persisted || j.Result.RecipientConnected == nil || *j.Result.RecipientConnected {
		t.Fatal("receipt facts lost", j, err)
	}
	if _, err := s.Reserve(ctx, identity.ID(), "request", SendAttempt, now); !errors.Is(err, ErrNotReady) {
		t.Fatal("accepted-but-not-persisted was resent")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	j, err = s.Lookup(ctx, identity, "request")
	if err != nil || j.Receipt == nil || j.Receipt.AccountID != r.AccountID || !j.Receipt.Timestamp.Equal(r.Timestamp) || j.Result.NextAction != "none" {
		t.Fatal("full receipt lost on reopen", j, err)
	}
	if _, err := s.Reserve(ctx, identity.ID(), "request", SendAttempt, now); !errors.Is(err, ErrNotReady) {
		t.Fatal("restart resent saved acceptance")
	}
}

func TestObservedAcceptanceSavesAfterRevocationAndExpiry(t *testing.T) {
	c, identity, _ := fixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := request()
	expires := int64(1005)
	r.ExpiresAt = &expires
	if _, _, err := s.Admit(ctx, identity, r, 0, now); err != nil {
		t.Fatal(err)
	}
	g, err := s.Reserve(ctx, identity.ID(), "request", SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	a, err := g.Consume(now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Revoke(ctx, identity.ID()); err != nil {
		t.Fatal(err)
	}
	j, err := s.RecordAcceptance(ctx, g, receiptFor(a), time.Unix(1004, 0), time.Unix(1010, 0))
	if err != nil || j.Result.State != core.Accepted {
		t.Fatal("known acceptance discarded after expiry/revoke", err)
	}
}

func TestAcceptanceRetryKeepsPrivateReceipt(t *testing.T) {
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
	a, err := g.Consume(now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER fixture_receipt BEFORE UPDATE OF receipt ON jobs BEGIN SELECT RAISE(ABORT,'fixture save failure'); END;`); err != nil {
		t.Fatal(err)
	}
	r := receiptFor(a)
	if _, err := s.RecordAcceptance(ctx, g, r, now, now); err == nil {
		t.Fatal("failed save claimed acceptance")
	}
	r.Message = "caller mutated"
	r.Receipt.Persisted = true
	if _, err := s.Reserve(ctx, identity.ID(), "request", SendAttempt, now); !errors.Is(err, ErrNotReady) {
		t.Fatal("failed save permitted resend")
	}
	if _, err := s.db.ExecContext(ctx, "DROP TRIGGER fixture_receipt"); err != nil {
		t.Fatal(err)
	}
	j, err := s.RetryAcceptance(ctx, g, time.Unix(1040, 0))
	if err != nil || j.Receipt.Message != a.Job.Request.Message || *j.Result.Persisted {
		t.Fatal("save retry depended on caller object", err)
	}
	if _, err := s.RetryAcceptance(ctx, g, time.Unix(1041, 0)); err != nil {
		t.Fatal("same save not idempotent", err)
	}
}
