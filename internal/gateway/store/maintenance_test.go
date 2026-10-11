//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestMaintenanceExpiryPreservesUncertaintyAndActiveAcceptance(t *testing.T) {
	c, identity, _ := fixture(t)
	c.Retry.LifetimeSeconds, c.Retry.TerminalRetentionSeconds = 10, 10
	c.Limits.Pending, c.Limits.PendingPerAuthor = 3, 3
	ctx := context.Background()
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Unix(1000, 0)
	expires := int64(1005)
	for _, id := range []string{"idle", "uncertain", "active"} {
		r := request()
		r.RequestID, r.ExpiresAt = id, &expires
		if _, _, err := s.AdmitAuthor(ctx, identity, r, 0, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.AdmitAuthor(ctx, identity, request(), 0, now); !errors.Is(err, ErrCapacity) {
		t.Fatal("pending quota not enforced", err)
	}
	g, err := s.Reserve(ctx, scopeOf(s, identity), "uncertain", SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Consume(now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Complete(ctx, g, Completion{Failure: core.Failure{Code: core.DeliveryUncertain, Transient: true}, MayHavePosted: true}, now); err != nil {
		t.Fatal(err)
	}
	g, err = s.Reserve(ctx, scopeOf(s, identity), "active", SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	a, err := g.Consume(now)
	if err != nil {
		t.Fatal(err)
	}
	counts, err := s.Maintain(ctx, time.Unix(expires, 0))
	if err != nil || counts.Expired != 2 {
		t.Fatal("idle expiry counts", counts, err)
	}
	for _, id := range []string{"idle", "uncertain"} {
		j, err := s.LookupAuthor(ctx, identity, id)
		if err != nil || j.Result.State != core.Expired || j.Result.NextAction != "none" || j.Result.AcceptanceUncertain != (id == "uncertain") {
			t.Fatal("expiry lost evidence", j, err)
		}
		if id == "idle" && (j.Result.Accepted == nil || *j.Result.Accepted) || id == "uncertain" && j.Result.Accepted != nil {
			t.Fatal("expiry misreported acceptance", j)
		}
	}
	if _, _, err := s.AdmitAuthor(ctx, identity, request(), 0, time.Unix(expires, 0)); err != nil {
		t.Fatal("known no-write expiry did not release quota", err)
	}
	j, err := s.RecordAcceptance(ctx, g, receiptFor(a), now.Add(time.Second), time.Unix(expires+1, 0))
	if err != nil || j.Result.State != core.Accepted {
		t.Fatal("maintenance fenced valid in-flight acceptance", j, err)
	}
	counts, err = s.Maintain(ctx, time.Unix(1100, 0))
	if err != nil || counts.Results != 2 {
		t.Fatal("resolved results did not prune", counts, err)
	}
	j, err = s.LookupAuthor(ctx, identity, "uncertain")
	if err != nil || !j.Result.AcceptanceUncertain {
		t.Fatal("unresolved expiry was pruned", j, err)
	}
}
