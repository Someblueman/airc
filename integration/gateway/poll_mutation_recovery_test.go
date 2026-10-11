//go:build darwin || linux

package gateway_test

import (
	"context"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/delivery"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestPollMutationCreationRecoveryAndReceiptOnly(t *testing.T) {
	for _, policy := range []core.Policy{core.Retry, core.ReceiptOnly} {
		t.Run(string(policy), func(t *testing.T) {
			socket := fixture(t)
			provision := dial(t, socket, "fixture-author", fixtureAccount, true, true)
			_ = provision.Close()
			c, _ := acceptanceJournalFixture(t, socket, fixtureAccount)
			c.Retry.BackoffSeconds, c.Retry.MaxBackoffSeconds = 1, 1
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
			target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
			observer := newReplySignalObserver(t, socket, *target)
			r := pollMutationRequest("poll-recovery", core.PollCreate, target, "")
			r.DeliveryPolicy = &policy
			_, original := replyAttempt(t, s, b, identity, r)
			if original.Poll == nil {
				t.Fatalf("native poll creation missing: %+v", original)
			}
			firstID := original.Poll.Poll.ID
			next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == firstID && e.Kind == "poll" })
			// Retain the durable dispatch marker but lose the observed compound ACK.
			if err = s.Close(); err != nil {
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
			defer s.Close()
			runner, err := delivery.New(s, b, c.Limits.Workers)
			if err != nil {
				t.Fatal(err)
			}
			workCtx, cancel := context.WithCancel(ctx)
			done := make(chan error, 1)
			go func() { done <- runner.Run(workCtx) }()
			defer func() {
				cancel()
				select {
				case err := <-done:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(7 * time.Second):
					t.Error("poll recovery did not join")
				}
			}()
			j := awaitDelivery(t, s, identity, r.RequestID, func(j store.Job) bool {
				return policy == core.Retry && j.Result.State == core.Accepted || policy == core.ReceiptOnly && j.Result.NextAction == "none"
			})
			if j.Result.LookupAttempts != 0 || !j.Result.DuplicateRisk {
				t.Fatalf("poll recovery facts: %+v", j.Result)
			}
			if policy == core.Retry {
				if j.Result.SendAttempts != 2 || j.Result.Poll == nil || j.Result.Poll.Poll.ID == firstID {
					t.Fatalf("poll retry failed to preserve duplicate: %+v", j.Result)
				}
				second := next(t, observer, func(e *irc.MessageEvent) bool { return e.Kind == "poll" && e.ID == j.Result.Poll.Poll.ID })
				if second.Message != r.Poll.Question {
					t.Fatal("retried poll changed question", second)
				}
				old, new := nativePollEntry(t, socket, firstID), nativePollEntry(t, socket, j.Result.Poll.Poll.ID)
				if old.ID == new.ID || old.Text != new.Text {
					t.Fatal("native duplicate creation not retained", old, new)
				}
			} else {
				if j.Result.SendAttempts != 1 || !j.Result.AcceptanceUncertain || j.Result.Poll != nil || j.Result.State == core.Accepted {
					t.Fatalf("receipt-only fabricated poll evidence: %+v", j.Result)
				}
				assertNoPollMutation(t, observer, firstID)
			}
		})
	}
}
