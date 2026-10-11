//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestGrantDeadlineAndFailedMarker(t *testing.T) {
	c, identity, _ := fixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := request()
	expiry := int64(1005)
	r.ExpiresAt = &expiry
	if _, _, err := s.AdmitAuthor(ctx, identity, r, 0, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER fixture_marker BEFORE UPDATE OF attempt_id ON jobs BEGIN SELECT RAISE(ABORT,'fixture marker failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if g, err := s.Reserve(ctx, scopeOf(s, identity), r.RequestID, SendAttempt, now); err == nil || g != nil {
		t.Fatal("failed marker produced a grant")
	}
	j, err := s.LookupAuthor(ctx, identity, r.RequestID)
	if err != nil || j.Result.SendAttempts != 0 {
		t.Fatal("failed marker consumed budget", err)
	}
	if _, err := s.db.ExecContext(ctx, "DROP TRIGGER fixture_marker"); err != nil {
		t.Fatal(err)
	}
	g, err := s.Reserve(ctx, scopeOf(s, identity), r.RequestID, SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Consume(time.Unix(1005, 0)); !errors.Is(err, ErrNotReady) {
		t.Fatal("expired grant consumed")
	}
}

func TestReceiptOnlyRestartAndLookupExhaustion(t *testing.T) {
	c, identity, _ := fixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	c.Retry.Policy = core.ReceiptOnly
	c.Retry.LookupAttempts = 1
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AdmitAuthor(ctx, identity, request(), 0, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reserve(ctx, scopeOf(s, identity), "request", SendAttempt, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	j, err := s.LookupAuthor(ctx, identity, "request")
	if err != nil || j.Result.State != core.Uncertain {
		t.Fatal("receipt-only crash was not uncertain", err)
	}
	if _, err := s.Reserve(ctx, scopeOf(s, identity), "request", SendAttempt, now); !errors.Is(err, ErrNotReady) {
		t.Fatal("receipt-only retry allowed")
	}
	if _, err := s.Reserve(ctx, scopeOf(s, identity), "request", LookupAttempt, now); err != nil {
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
	if _, err := s.Reserve(ctx, scopeOf(s, identity), "request", LookupAttempt, now); !errors.Is(err, ErrNotReady) {
		t.Fatal("restart reset lookup budget")
	}
}
