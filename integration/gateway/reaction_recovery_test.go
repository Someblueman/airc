package gateway_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/delivery"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestReactionRecoveryRetainedTupleAndReceiptOnly(t *testing.T) {
	for _, test := range []struct {
		name   string
		policy core.Policy
		evict  bool
	}{{"retry", core.Retry, false}, {"receipt_only", core.ReceiptOnly, false}, {"evicted_parent", core.Retry, true}} {
		t.Run(test.name, func(t *testing.T) {
			policy := test.policy
			socket := fixture(t)
			const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
			provision := dial(t, socket, "fixture-author", token, true, true)
			_ = provision.Close()
			author := dial(t, socket, "fixture-author", token, false, true)
			if err := author.SendWithID("#gateway-fixture", "reaction recovery parent", "reaction-parent"); err != nil {
				t.Fatal(err)
			}
			parent := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == "reaction-parent" })
			c, _ := acceptanceJournalFixture(t, socket, token)
			c.Retry.BackoffSeconds, c.Retry.MaxBackoffSeconds, c.Retry.LookupAttempts = 1, 1, 1
			identity := fixtureIdentity(t, c)
			b, err := backend.New(c)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			s, err := store.Open(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			r := core.Request{Version: 1, RequestID: "reaction-recovery", Operation: core.ReactionAdd, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, ParentID: parent.ID, Reaction: "agree", DeliveryPolicy: &policy}
			_, original := replyAttempt(t, s, b, identity, r)
			if original.Receipt == nil {
				t.Fatal("native reaction not accepted", original)
			}
			if test.evict {
				// The pinned fixture retains 32 messages. Evict the older parent
				// and its newer reaction together; retry must not recreate either.
				for i := range 32 {
					id := "eviction-" + strconv.Itoa(i)
					if err := author.SendWithID("#gateway-fixture", id, id); err != nil {
						t.Fatal(err)
					}
					next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == id })
				}
			}
			// Lose the already observed acceptance at the journal boundary. Reopen
			// recovers uncertainty and the spent budget rather than replaying intake.
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = store.Open(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			// A registered author's token lives only in memory: resubmitting
			// the request resumes the job the reopen parked.
			if _, _, err := s.AdmitAuthor(ctx, identity, r, 0, time.Now()); err != nil {
				t.Fatal(err)
			}
			runner, err := delivery.New(s, b, c.Limits.Workers)
			if err != nil {
				t.Fatal(err)
			}
			workCtx, cancel := context.WithCancel(ctx)
			done := make(chan error, 1)
			go func() { done <- runner.Run(workCtx) }()
			t.Cleanup(func() {
				cancel()
				select {
				case err := <-done:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(7 * time.Second):
					t.Error("reaction recovery did not join")
				}
			})
			j := awaitDelivery(t, s, identity, r.RequestID, func(j store.Job) bool {
				if test.evict {
					return j.Result.State == core.Rejected
				}
				return policy == core.Retry && j.Result.State == core.Accepted || policy == core.ReceiptOnly && j.Result.LookupAttempts == 1 && j.Result.NextAction == "none"
			})
			if test.evict {
				if j.Result.SendAttempts != 2 || j.Result.LookupAttempts != 1 || !j.Result.AcceptanceUncertain || !j.Result.DuplicateRisk || j.Result.Failure == nil || j.Result.Failure.Code != core.InvalidRequest || j.Result.Failure.UpstreamCode != 461 {
					t.Fatal("evicted parent retry lost earlier uncertainty or no-write rejection", j)
				}
			} else if policy == core.Retry {
				if j.Result.MessageID != original.Receipt.ID || j.Result.SendAttempts != 2 || j.Result.LookupAttempts != 1 || !j.Result.DuplicateRisk {
					t.Fatal("retained tuple retry lost evidence or uncertainty", j)
				}
			} else if j.Result.SendAttempts != 1 || !j.Result.AcceptanceUncertain || j.Result.State == core.Accepted {
				t.Fatal("receipt-only recovery wrote or invented acceptance", j)
			}
			readCtx, finish := context.WithTimeout(ctx, 3*time.Second)
			defer finish()
			entries, err := irc.RequestCheck(readCtx, author, irc.CheckRequest{Targets: []irc.CheckTarget{{Target: "#gateway-fixture", After: "*", Limit: 32}}, IncludeOwn: true, MaxMessages: 32}, nil)
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, entry := range entries {
				if entry.Message != nil && entry.Message.ReplyTo == parent.ID && entry.Message.Reaction == r.Reaction {
					count++
					if entry.Message.ID != original.Receipt.ID {
						t.Fatal("retained native tuple produced another reaction", entry)
					}
				}
			}
			want := 1
			if test.evict {
				want = 0
			}
			if count != want {
				t.Fatal("native reaction history mismatch", count)
			}
		})
	}
}
