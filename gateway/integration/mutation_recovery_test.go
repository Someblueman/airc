//go:build darwin || linux

package gateway_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/delivery"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/pkg/irc"
)

// recoveryCase is one mutation whose native write succeeded but whose
// acknowledgement the journal lost in a crash.
type recoveryCase struct {
	name string
	// request seeds native state and returns the mutation.
	request func(t *testing.T, socket string, observer *irc.Client) core.Request
	// effect consumes the observer's broadcast of a successful write and
	// returns a comparable witness of it ("" when there is none).
	effect func(t *testing.T, observer *irc.Client, r core.Request, outcome backend.Outcome) string
	// throttled writes are visible again only after the native two-second
	// signal throttle.
	throttled bool
	// messageless mutations never report message receipt facts.
	messageless bool
	// retried checks the native state and acknowledgement after the retry.
	retried func(t *testing.T, socket string, r core.Request, first string, job store.Job)
	// final checks native state for both policies.
	final func(t *testing.T, socket string, r core.Request)
}

func recoveryCases() []recoveryCase {
	room := func() *core.Target { return &core.Target{Kind: "room", Name: "#gateway-fixture"} }
	message := func(op core.Operation) recoveryCase {
		return recoveryCase{
			name: string(op),
			request: func(t *testing.T, socket string, _ *irc.Client) core.Request {
				parent := ""
				if op != core.MessageAction {
					parent = seedPinMutationMessage(t, socket, "#gateway-fixture", "post-recovery-parent")
				}
				return messageMutationRequest("post-recovery", op, room(), parent)
			},
			effect: func(t *testing.T, observer *irc.Client, r core.Request, o backend.Outcome) string {
				if o.Receipt == nil {
					t.Fatalf("native message creation missing: %+v", o)
				}
				body := r.Message
				if op == core.MessageRetract && body == "" {
					body = "Retracted"
				}
				seen := next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == o.Receipt.ID && e.Kind == mutationKind(op) })
				if seen.Message != body {
					t.Fatalf("native message body changed: %+v", seen)
				}
				return o.Receipt.ID
			},
			retried: func(t *testing.T, _ string, r core.Request, first string, j store.Job) {
				// A message retry is a second native message: the duplicate risk.
				if j.Receipt == nil || j.Result.MessageID == first {
					t.Fatalf("message retry did not record its duplicate: %+v", j.Result)
				}
			},
		}
	}
	pin := func(op core.Operation) recoveryCase {
		return recoveryCase{
			name:        string(op),
			messageless: true,
			request: func(t *testing.T, socket string, _ *irc.Client) core.Request {
				parent := seedPinMutationMessage(t, socket, "#gateway-fixture", "pin-recovery-parent")
				if op == core.PinsRemove {
					nativePinMutation(t, socket, "#gateway-fixture", parent, "pin")
				}
				return core.Request{Version: 1, RequestID: "pin-recovery", Operation: op, Target: room(), ParentID: parent}
			},
			effect: func(t *testing.T, observer *irc.Client, _ core.Request, o backend.Outcome) string {
				if o.Pin == nil {
					t.Fatalf("native pin mutation not accepted: %+v", o)
				}
				// The seeded parent and any pin notice precede recovery.
				drainObserver(observer)
				return fmt.Sprint(*o.Pin)
			},
			retried: func(t *testing.T, _ string, _ core.Request, first string, j store.Job) {
				if j.Result.Pin == nil || fmt.Sprint(*j.Result.Pin) != first {
					t.Fatalf("bounded pin retry lost acknowledgement: %+v", j.Result)
				}
			},
			final: func(t *testing.T, socket string, r core.Request) {
				pins := queryPinMutationNative(t, socket, r.Target.Name)
				if op == core.PinsAdd && (len(pins) != 1 || pins[0].ID != r.ParentID) || op == core.PinsRemove && len(pins) != 0 {
					t.Fatalf("pin recovery native state: %v", pins)
				}
			},
		}
	}
	activity := func(kind string) recoveryCase {
		return recoveryCase{
			name:        "activity." + kind,
			throttled:   true,
			messageless: true,
			request: func(*testing.T, string, *irc.Client) core.Request {
				return activityRequest("activity-recovery", kind, "recovery note", 15)
			},
			effect: func(t *testing.T, observer *irc.Client, _ core.Request, o backend.Outcome) string {
				signal := nextActivitySignal(t, observer)
				if o.Activity == nil || signal.Action != o.Activity.Kind || signal.Text != o.Activity.Note || !signal.ExpiresAt.Equal(o.Activity.ExpiresAt) {
					t.Fatal("initial native ACK/broadcast mismatch", signal, o.Activity)
				}
				return expiryWitness(signal.ExpiresAt)
			},
			retried: func(t *testing.T, _ string, r core.Request, _ string, j store.Job) {
				if j.Result.Activity == nil || j.Result.Activity.Kind != r.Activity.Kind || j.Result.Activity.Note != r.Activity.Note {
					t.Fatalf("activity retry lost intent: %+v", j.Result)
				}
			},
		}
	}
	signal := func(op core.Operation) recoveryCase {
		return recoveryCase{
			name:        string(op),
			throttled:   true,
			messageless: true,
			request: func(t *testing.T, socket string, observer *irc.Client) core.Request {
				parent := seedPinMutationMessage(t, socket, "#gateway-fixture", "signal-recovery-parent")
				if op == core.ReplySignalCancel {
					setReplySignalNative(t, socket, parent, &core.ReplySignalInput{Note: "before crash", TTLSeconds: 120})
					nextReplySignal(t, observer, parent)
				}
				return replySignalRequest("signal-recovery", parent, op, room())
			},
			effect: func(t *testing.T, observer *irc.Client, r core.Request, o backend.Outcome) string {
				signal := nextReplySignal(t, observer, r.ParentID)
				if o.ReplySignal == nil || signal.Action != o.ReplySignal.Action || signal.Target != o.ReplySignal.NativeTarget || signal.From != o.ReplySignal.From || signal.AccountID != o.ReplySignal.AccountID || signal.Text != o.ReplySignal.Note || !signal.ExpiresAt.Equal(o.ReplySignal.ExpiresAt) {
					t.Fatal("native reply signal ACK/broadcast mismatch", signal, o.ReplySignal)
				}
				return expiryWitness(signal.ExpiresAt)
			},
			retried: func(t *testing.T, _ string, r core.Request, _ string, j store.Job) {
				if j.Result.ReplySignal == nil || j.Result.ReplySignal.Action != map[core.Operation]string{core.ReplySignalSet: "prepare", core.ReplySignalCancel: "cancel"}[op] {
					t.Fatalf("reply signal retry lost intent: %+v", j.Result)
				}
			},
			final: func(t *testing.T, socket string, r core.Request) {
				waiting := queryNativeWaitingSignal(t, socket, r.ParentID)
				if op == core.ReplySignalSet && (len(waiting) != 1 || waiting[0].Action != "prepare") || op == core.ReplySignalCancel && len(waiting) != 0 {
					t.Fatalf("native crash recovery state wrong: %+v", waiting)
				}
			},
		}
	}
	return []recoveryCase{
		message(core.MessageAction), message(core.MessageCorrect), message(core.MessageRetract),
		pin(core.PinsAdd), pin(core.PinsRemove),
		activity("typing"), activity("thinking"),
		signal(core.ReplySignalSet), signal(core.ReplySignalCancel),
		{
			name: "poll.create",
			request: func(*testing.T, string, *irc.Client) core.Request {
				return pollMutationRequest("poll-recovery", core.PollCreate, room(), "")
			},
			effect: func(t *testing.T, observer *irc.Client, r core.Request, o backend.Outcome) string {
				if o.Poll == nil {
					t.Fatalf("native poll creation missing: %+v", o)
				}
				if seen := next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == o.Poll.Poll.ID && e.Kind == "poll" }); seen.Message != r.Poll.Question {
					t.Fatal("native poll question changed", seen)
				}
				return o.Poll.Poll.ID
			},
			retried: func(t *testing.T, socket string, r core.Request, first string, j store.Job) {
				if j.Result.Poll == nil || j.Result.Poll.Poll.ID == first {
					t.Fatalf("poll retry did not record its duplicate: %+v", j.Result)
				}
				if old, retried := nativePollEntry(t, socket, first), nativePollEntry(t, socket, j.Result.Poll.Poll.ID); old.Text != retried.Text || retried.Text != r.Poll.Question {
					t.Fatal("native duplicate creation not retained", old, retried)
				}
			},
		},
	}
}

// expiryWitness orders native expiries as strings.
func expiryWitness(at time.Time) string {
	return at.UTC().Format("2006-01-02T15:04:05.000000000")
}

func mutationKind(op core.Operation) string {
	return map[core.Operation]string{core.MessageAction: "action", core.MessageCorrect: "correct", core.MessageRetract: "retract"}[op]
}

// TestMutationCrashRecoveryRetryAndReceiptOnly writes each mutation natively,
// then reopens the journal as a crash would leave it: the dispatch marker
// survives but the observed acknowledgement does not. A retry policy writes
// once more and records the duplicate risk; receipt_only never resends and
// reports the outcome as uncertain without inventing evidence.
func TestMutationCrashRecoveryRetryAndReceiptOnly(t *testing.T) {
	t.Parallel()
	for _, c := range recoveryCases() {
		for _, policy := range []core.Policy{core.Retry, core.ReceiptOnly} {
			t.Run(c.name+"/"+string(policy), func(t *testing.T) {
				socket := fixture(t)
				provision := dial(t, socket, "fixture-author", fixtureAccount, true, true)
				_ = provision.Close()
				observer := newReplySignalObserver(t, socket, core.Target{Kind: "room", Name: "#gateway-fixture"})
				config, _ := acceptanceJournalFixture(t, socket, fixtureAccount)
				config.Retry.BackoffSeconds, config.Retry.MaxBackoffSeconds = 1, 1
				identity := fixtureIdentity(t, config)
				b, err := backend.New(config)
				if err != nil {
					t.Fatal(err)
				}
				ctx := context.Background()
				s, err := store.Open(ctx, config)
				if err != nil {
					t.Fatal(err)
				}
				r := c.request(t, socket, observer)
				r.DeliveryPolicy = &policy
				_, original := replyAttempt(t, s, b, identity, r)
				first := c.effect(t, observer, r, original)
				// Crash at the durable evidence boundary: the native write
				// happened, but the journal holds only its dispatch marker.
				if err = s.Close(); err != nil {
					t.Fatal(err)
				}
				if s, err = store.Open(ctx, config); err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				// A registered author's token lives only in memory: resubmitting
				// the request resumes the job the reopen parked.
				if _, _, err := s.AdmitAuthor(ctx, identity, r, 0, time.Now()); err != nil {
					t.Fatal(err)
				}
				if c.throttled && policy == core.Retry {
					// A retried signal is only visible after the native throttle.
					time.Sleep(2100 * time.Millisecond)
				}
				runner, err := delivery.New(s, b, config.Limits.Workers)
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
						t.Error("recovery runner did not join")
					}
				}()
				j := awaitDelivery(t, s, identity, r.RequestID, func(j store.Job) bool {
					return policy == core.Retry && j.Result.State == core.Accepted || policy == core.ReceiptOnly && j.Result.NextAction == "none"
				})
				result := j.Result
				if result.LookupAttempts != 0 || !result.DuplicateRisk || c.messageless && (result.MessageID != "" || result.Persisted != nil || result.RecipientConnected != nil) {
					t.Fatalf("recovery facts: %+v", result)
				}
				if policy == core.Retry {
					if result.SendAttempts != 2 || result.Accepted == nil || !*result.Accepted {
						t.Fatalf("retry was not accepted once more: %+v", result)
					}
					c.retried(t, socket, r, first, j)
					// The retry is an actual native write, seen like the first.
					var retriedOutcome backend.Outcome
					retriedOutcome.Receipt, retriedOutcome.Pin, retriedOutcome.Activity, retriedOutcome.ReplySignal, retriedOutcome.Poll = j.Receipt, result.Pin, result.Activity, result.ReplySignal, result.Poll
					if second := c.effect(t, observer, r, retriedOutcome); c.throttled && second <= first {
						t.Fatalf("retried signal did not refresh its native expiry: first=%s second=%s", first, second)
					}
				} else {
					if result.SendAttempts != 1 || !result.AcceptanceUncertain || result.State == core.Accepted || j.Receipt != nil || result.MessageID != "" || result.Pin != nil || result.Activity != nil || result.ReplySignal != nil || result.Poll != nil {
						t.Fatalf("receipt_only resent or invented evidence: %+v", result)
					}
					assertNoNativeMutation(t, observer)
				}
				if c.final != nil {
					c.final(t, socket, r)
				}
			})
		}
	}
}
