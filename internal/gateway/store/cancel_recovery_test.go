//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// TestCancelledCallLeavesNoTransactionOpen cancels store calls while their
// transaction is open, then at once uses the store with a live context, as
// the delivery runner's shutdown save does after its poll is cancelled.
// database/sql rolls a Tx back from its own goroutine when the Tx's context
// ends; if that rollback could still be pending after the call returned, the
// next BEGIN on the store's one connection would fail with "cannot start a
// transaction within a transaction".
func TestCancelledCallLeavesNoTransactionOpen(t *testing.T) {
	c, identity, _ := fixture(t)
	ctx := context.Background()
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	r := request()
	now := time.Unix(1000, 0)
	if _, _, err := s.AdmitAuthor(ctx, identity, r, 0, now); err != nil {
		t.Fatal(err)
	}
	for i := range 600 {
		call, cancel := context.WithCancel(ctx)
		go cancel()
		switch i % 3 {
		case 0:
			_, _ = s.Ready(call, "", 4, now)
		case 1:
			_, _ = s.LookupAuthor(call, identity, r.RequestID)
		default:
			_, _ = s.Maintain(call, now)
		}
		cancel()
		if _, err := s.LookupAuthor(ctx, identity, r.RequestID); err != nil {
			t.Fatalf("call %d after a cancelled transaction: %v", i, err)
		}
	}
}

// TestFullJournalRecoversWhenSpaceReturns makes the journal full with SQLite's
// own page limit (a stand-in for a full disk: both fail with SQLITE_FULL), then
// lifts it. A failed admission must leave no partial job and no transaction
// open, and the store must admit, reserve, complete, and look up afterwards.
func TestFullJournalRecoversWhenSpaceReturns(t *testing.T) {
	c, identity, _ := fixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, _, err := s.AdmitAuthor(ctx, identity, request(), 0, now); err != nil {
		t.Fatal(err)
	}
	var pages, capacity int64
	if err := s.db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pages); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, "PRAGMA max_page_count").Scan(&capacity); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf("PRAGMA max_page_count=%d", pages)).Scan(&pages); err != nil {
		t.Fatal(err)
	}
	refused := request()
	refused.Message = strings.Repeat("x", 4096)
	for i := 0; ; i++ {
		if i == c.Limits.PendingPerAuthor-1 {
			t.Fatal("journal never filled")
		}
		refused.RequestID = fmt.Sprintf("fill-%d", i)
		_, _, err := s.AdmitAuthor(ctx, identity, refused, 0, now)
		if err == nil {
			continue
		}
		if !strings.Contains(err.Error(), "full (13)") {
			t.Fatal("full journal failed otherwise", err)
		}
		break
	}
	if _, err := s.LookupAuthor(ctx, identity, refused.RequestID); !errors.Is(err, ErrNotFound) {
		t.Fatal("refused admission left a job", err)
	}
	if _, err := s.Maintain(ctx, now); err != nil {
		t.Fatal("full journal stopped maintenance", err)
	}
	var restored int64
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf("PRAGMA max_page_count=%d", capacity)).Scan(&restored); err != nil || restored != capacity {
		t.Fatal("capacity not restored", restored, err)
	}
	if _, created, err := s.AdmitAuthor(ctx, identity, refused, 0, now); err != nil || !created {
		t.Fatal("admission after space returned", created, err)
	}
	g, _ := consumeAttempt(t, s, scopeOf(s, identity), SendAttempt, now)
	if _, err := s.Complete(ctx, g, Completion{Failure: core.Failure{Code: core.UpstreamUnavailable, Transient: true}, RetryAfterSeconds: 10}, now); err != nil {
		t.Fatal("completion after space returned", err)
	}
	if j, err := s.LookupAuthor(ctx, identity, "request"); err != nil || j.Result.State != core.RetryWait {
		t.Fatal("lookup after space returned", j, err)
	}
}
