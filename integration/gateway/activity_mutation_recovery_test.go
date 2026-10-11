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
)

func TestActivityMutationRecoveryAndReceiptOnly(t *testing.T) {
	for _, kind := range []string{"typing", "thinking"} {
		for _, policy := range []core.Policy{core.Retry, core.ReceiptOnly} {
			t.Run(kind+"/"+string(policy), func(t *testing.T) {
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
				r := activityRequest("activity-recovery", kind, "recovery note", 15)
				r.DeliveryPolicy = &policy
				observer := newActivityObserver(t, socket, *r.Target)
				_, original := replyAttempt(t, s, b, identity, r)
				if original.Activity == nil {
					t.Fatalf("native activity missing: %+v", original)
				}
				first := nextActivitySignal(t, observer)
				if first.Action != original.Activity.Kind || first.Text != original.Activity.Note || !first.ExpiresAt.Equal(original.Activity.ExpiresAt) {
					t.Fatal("initial native ACK/broadcast mismatch", first, original.Activity)
				}
				// Leave the durable dispatch marker while dropping observed acceptance.
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
				if policy == core.Retry {
					// A retry must be visible outside the native two-second throttle.
					time.Sleep(2100 * time.Millisecond)
				}
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
						t.Error("activity recovery did not join")
					}
				}()
				job := awaitDelivery(t, s, identity, r.RequestID, func(j store.Job) bool {
					return policy == core.Retry && j.Result.State == core.Accepted || policy == core.ReceiptOnly && j.Result.NextAction == "none"
				})
				result := job.Result
				if result.LookupAttempts != 0 || !result.DuplicateRisk || result.MessageID != "" || result.Persisted != nil || result.RecipientConnected != nil {
					t.Fatalf("activity recovery facts: %+v", result)
				}
				if policy == core.Retry {
					second := nextActivitySignal(t, observer)
					if result.SendAttempts != 2 || result.Activity == nil || result.Accepted == nil || !*result.Accepted || result.Activity.Kind != kind || result.Activity.Note != r.Activity.Note || second.Action != result.Activity.Kind || second.Text != result.Activity.Note || !second.ExpiresAt.Equal(result.Activity.ExpiresAt) || !second.ExpiresAt.After(first.ExpiresAt) {
						t.Fatalf("retry did not refresh native activity: result=%+v first=%+v second=%+v", result, first, second)
					}
				} else {
					if result.SendAttempts != 1 || !result.AcceptanceUncertain || result.Activity != nil || result.State == core.Accepted {
						t.Fatalf("receipt-only invented evidence or retried: %+v", result)
					}
					assertNoActivitySignal(t, observer)
				}
			})
		}
	}
}
