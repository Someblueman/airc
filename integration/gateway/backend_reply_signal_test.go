//go:build darwin || linux

package gateway_test

import (
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestBackendReplySignalSetCancelMatchesNativeWaiting(t *testing.T) {
	socket := fixture(t)
	provision := dial(t, socket, "fixture-author", fixtureAccount, true, true)
	_ = provision.Close()
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	parent := seedPinMutationMessage(t, socket, target.Name, "shared-signal-parent")
	observer := newReplySignalObserver(t, socket, *target)
	c, _ := acceptanceJournalFixture(t, socket, fixtureAccount)
	identity := fixtureIdentity(t, c)
	b, err := backend.New(c)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := store.Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	for _, op := range []core.Operation{core.ReplySignalSet, core.ReplySignalCancel} {
		id := "shared-signal-set"
		if op == core.ReplySignalCancel {
			id = "shared-signal-cancel"
		}
		request := replySignalRequest(id, parent, op, target)
		if request.ReplySignal != nil {
			request.ReplySignal.TTLSeconds = 5
		}
		grant, outcome := replyAttempt(t, journal, b, identity, request)
		if outcome.ReplySignal == nil {
			t.Fatalf("native reply signal acknowledgement missing: %+v", outcome)
		}
		job, err := journal.RecordReplySignalAcceptance(t.Context(), grant, outcome.ReplySignal, outcome.ObservedAt, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		assertReplySignalFacts(t, job.Result, request, nextReplySignal(t, observer, parent))
		waiting := queryNativeWaitingSignal(t, socket, parent)
		if op == core.ReplySignalSet {
			ack := job.Result.ReplySignal
			if len(waiting) != 1 || waiting[0].ID != parent || waiting[0].Action != ack.Action || waiting[0].Text != ack.Note || !waiting[0].ExpiresAt.Equal(ack.ExpiresAt) {
				t.Fatalf("native waiting differs from durable preparation: %+v ack=%+v", waiting, ack)
			}
		} else if len(waiting) != 0 {
			t.Fatalf("native cancellation left waiting state: %+v", waiting)
		}
	}
}
