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

func TestMessageMutationRecoveryAndReceiptOnly(t *testing.T) {
	for _, op := range []core.Operation{core.MessageAction, core.MessageCorrect, core.MessageRetract} {
		for _, policy := range []core.Policy{core.Retry, core.ReceiptOnly} {
			t.Run(string(op)+"/"+string(policy), func(t *testing.T) {
				socket := fixture(t)
				provision := dial(t, socket, "fixture-author", fixtureAccount, true, true)
				_ = provision.Close()
				c, _ := acceptanceJournalFixture(t, socket, fixtureAccount)
				c.Principals[0].Operations = append(c.Principals[0].Operations, op)
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
				parent := ""
				if op != core.MessageAction {
					parent = seedPinMutationMessage(t, socket, target.Name, "post-recovery-parent")
				}
				r := messageMutationRequest("post-recovery", op, target, parent)
				r.DeliveryPolicy = &policy
				_, original := replyAttempt(t, s, b, identity, r)
				if original.Receipt == nil {
					t.Fatalf("native message creation missing: %+v", original)
				}
				firstID := original.Receipt.ID
				next(t, observer, func(e *irc.MessageEvent) bool {
					return e.ID == firstID && (e.Kind == "action" || e.Kind == "correct" || e.Kind == "retract")
				})
				// Retain the durable dispatch marker but lose the observed complete CHAT response.
				if err = s.Close(); err != nil {
					t.Fatal(err)
				}
				s, err = store.Open(ctx, c)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				time.Sleep(1100 * time.Millisecond)
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
						t.Error("message recovery did not join")
					}
				}()
				j := awaitDelivery(t, s, identity, r.RequestID, func(j store.Job) bool {
					return policy == core.Retry && j.Result.State == core.Accepted || policy == core.ReceiptOnly && j.Result.NextAction == "none"
				})
				if j.Result.LookupAttempts != 0 || !j.Result.DuplicateRisk {
					t.Fatalf("message recovery facts: %+v", j.Result)
				}
				if policy == core.Retry {
					if j.Result.SendAttempts != 2 || j.Receipt == nil || j.Result.MessageID == firstID {
						t.Fatalf("message retry failed to preserve duplicate: %+v", j.Result)
					}
					second := next(t, observer, func(e *irc.MessageEvent) bool {
						return e.ID == j.Result.MessageID && (e.Kind == "action" || e.Kind == "correct" || e.Kind == "retract")
					})
					expected := r.Message
					if op == core.MessageRetract && expected == "" {
						expected = "Retracted"
					}
					if second.Message != expected {
						t.Fatal("retried message changed body", second)
					}
				} else {
					if j.Result.SendAttempts != 1 || !j.Result.AcceptanceUncertain || j.Receipt != nil || j.Result.State == core.Accepted {
						t.Fatalf("receipt-only fabricated message evidence: %+v", j.Result)
					}
					assertNoMessageMutation(t, observer)
				}
			})
		}
	}
}
