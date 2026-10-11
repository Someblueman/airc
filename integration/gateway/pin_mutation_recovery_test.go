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

func TestPinMutationRecoveryAndReceiptOnly(t *testing.T) {
	for _, operation := range []core.Operation{core.PinsAdd, core.PinsRemove} {
		for _, policy := range []core.Policy{core.Retry, core.ReceiptOnly} {
			t.Run(string(operation)+"/"+string(policy), func(t *testing.T) {
				socket := fixture(t)
				provision := dial(t, socket, "fixture-author", fixtureAccount, true, true)
				_ = provision.Close()
				parent := seedPinMutationMessage(t, socket, "#gateway-fixture", "pin-recovery-parent")
				if operation == core.PinsRemove {
					seedNativePin(t, &reactionTransportFixture{socket: socket}, parent)
				}
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
				request := core.Request{Version: 1, RequestID: "pin-recovery", Operation: operation, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, ParentID: parent, DeliveryPolicy: &policy}
				_, original := replyAttempt(t, s, b, identity, request)
				if original.Pin == nil {
					t.Fatalf("native pin mutation not accepted: %+v", original)
				}
				// Crash at the durable evidence boundary: the native ACK was received,
				// but the journal still contains its dispatch marker.
				if err = s.Close(); err != nil {
					t.Fatal(err)
				}
				s, err = store.Open(ctx, c)
				if err != nil {
					t.Fatal(err)
				}
				// A registered author's token lives only in memory: resubmitting
				// the request resumes the job the reopen parked.
				if _, _, err := s.AdmitAuthor(ctx, identity, request, 0, time.Now()); err != nil {
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
						t.Error("pin recovery did not join")
					}
				}()
				job := awaitDelivery(t, s, identity, request.RequestID, func(job store.Job) bool {
					return policy == core.Retry && job.Result.State == core.Accepted || policy == core.ReceiptOnly && job.Result.NextAction == "none"
				})
				if job.Result.LookupAttempts != 0 || !job.Result.DuplicateRisk || job.Result.MessageID != "" || job.Result.Persisted != nil || job.Result.RecipientConnected != nil {
					t.Fatalf("pin recovery facts: %+v", job.Result)
				}
				if policy == core.Retry {
					if job.Result.SendAttempts != 2 || job.Result.Pin == nil || *job.Result.Pin != *original.Pin || job.Result.Accepted == nil || !*job.Result.Accepted {
						t.Fatalf("bounded pin retry lost acknowledgement: %+v", job.Result)
					}
				} else if job.Result.SendAttempts != 1 || !job.Result.AcceptanceUncertain || job.Result.Pin != nil || job.Result.State == core.Accepted {
					t.Fatalf("receipt-only invented acceptance or resent: %+v", job.Result)
				}
				pins := queryPinMutationNative(t, socket, request.Target.Name)
				if operation == core.PinsAdd && (len(pins) != 1 || pins[0].ID != parent) || operation == core.PinsRemove && len(pins) != 0 {
					t.Fatalf("pin recovery native state: %v", pins)
				}
			})
		}
	}
}
