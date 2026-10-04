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

func TestReplySignalRecoveryAndReceiptOnly(t *testing.T) {
	for _, op := range []core.Operation{core.ReplySignalSet, core.ReplySignalCancel} {
		for _, policy := range []core.Policy{core.Retry, core.ReceiptOnly} {
			t.Run(string(op)+"/"+string(policy), func(t *testing.T) {
				socket := fixture(t)
				provision := dial(t, socket, "fixture-author", fixtureAccount, true, true)
				_ = provision.Close()
				parent := seedPinMutationMessage(t, socket, "#gateway-fixture", "signal-recovery-parent")
				c, _ := acceptanceJournalFixture(t, socket, fixtureAccount)
				c.Principals[0].Operations = append(c.Principals[0].Operations, core.ReplySignalSet, core.ReplySignalCancel)
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
				if op == core.ReplySignalCancel {
					setReplySignalNative(t, socket, parent, &core.ReplySignalInput{Note: "before crash", TTLSeconds: 120})
					nextReplySignal(t, observer, parent)
				}
				r := replySignalRequest("signal-recovery", parent, op, target)
				r.DeliveryPolicy = &policy
				_, original := replyAttempt(t, s, b, identity, r)
				if original.ReplySignal == nil {
					t.Fatalf("native reply signal missing: %+v", original)
				}
				first := nextReplySignal(t, observer, parent)
				if first.Action != original.ReplySignal.Action || first.Target != original.ReplySignal.NativeTarget || first.ID != parent || first.Text != original.ReplySignal.Note || !first.ExpiresAt.Equal(original.ReplySignal.ExpiresAt) {
					t.Fatal("initial native ACK/broadcast mismatch", first, original.ReplySignal)
				}
				// Lose the observed ACK while retaining the durable dispatch marker.
				if err = s.Close(); err != nil {
					t.Fatal(err)
				}
				s, err = store.Open(ctx, c)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				time.Sleep(2100 * time.Millisecond)
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
						t.Error("reply signal recovery did not join")
					}
				}()
				j := awaitDelivery(t, s, identity, r.RequestID, func(j store.Job) bool {
					return policy == core.Retry && j.Result.State == core.Accepted || policy == core.ReceiptOnly && j.Result.NextAction == "none"
				})
				result := j.Result
				if result.LookupAttempts != 0 || !result.DuplicateRisk || result.MessageID != "" || result.Persisted != nil || result.RecipientConnected != nil {
					t.Fatalf("reply signal recovery facts: %+v", result)
				}
				if policy == core.Retry {
					second := nextReplySignal(t, observer, parent)
					if result.SendAttempts != 2 || result.ReplySignal == nil || result.Accepted == nil || !*result.Accepted || second.Action != result.ReplySignal.Action || second.Target != result.ReplySignal.NativeTarget || second.ID != parent || second.From != result.ReplySignal.From || second.AccountID != result.ReplySignal.AccountID || second.Text != result.ReplySignal.Note || !second.ExpiresAt.Equal(result.ReplySignal.ExpiresAt) || !second.ExpiresAt.After(first.ExpiresAt) {
						t.Fatalf("retry did not emit actual signal: result=%+v first=%+v second=%+v", result, first, second)
					}
				} else {
					if result.SendAttempts != 1 || !result.AcceptanceUncertain || result.ReplySignal != nil || result.State == core.Accepted {
						t.Fatalf("receipt-only invented ACK or resent: %+v", result)
					}
					assertNoReplySignal(t, observer)
				}
				waiting := queryNativeWaitingSignal(t, socket, parent)
				if op == core.ReplySignalSet && (len(waiting) != 1 || waiting[0].ID != parent || waiting[0].Action != "prepare") || op == core.ReplySignalCancel && len(waiting) != 0 {
					t.Fatalf("native crash recovery state wrong: %+v", waiting)
				}
			})
		}
	}
}
