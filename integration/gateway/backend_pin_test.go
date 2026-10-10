//go:build darwin || linux

package gateway_test

import (
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestBackendPinAddRemoveMatchesNativeSnapshot(t *testing.T) {
	socket := fixtureWithAdmin(t, true)
	provision := dial(t, socket, "fixture-author", fixtureAccount, true, true)
	_ = provision.Close()
	promoteRoomOperator(t, socket)
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	parent := seedPinMutationMessage(t, socket, target.Name, "shared-pin-parent")
	c, _ := acceptanceJournalFixture(t, socket, fixtureAccount)
	c.Principals[0].Operations = append(c.Principals[0].Operations, core.PinsAdd, core.PinsRemove)
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
	for _, op := range []core.Operation{core.PinsAdd, core.PinsRemove} {
		id := "shared-pin-add"
		if op == core.PinsRemove {
			id = "shared-pin-remove"
		}
		request := core.Request{Version: 1, RequestID: id, Operation: op, Target: target, ParentID: parent}
		grant, outcome := replyAttempt(t, journal, b, identity, request)
		if outcome.Pin == nil {
			t.Fatalf("native pin acknowledgement missing: %+v", outcome)
		}
		job, err := journal.RecordPinAcceptance(t.Context(), grant, outcome.Pin, outcome.ObservedAt, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		assertPinMutationFacts(t, job.Result, request)
		pins := queryPinMutationNative(t, socket, target.Name)
		if op == core.PinsAdd {
			if len(pins) != 1 || pins[0].ID != parent || pins[0].From != "fixture-author" || pins[0].AccountID == "" || pins[0].Target != target.Name || pins[0].Message != "pin mutation shared-pin-parent" {
				t.Fatalf("native pin snapshot differs from intended parent: %+v", pins)
			}
		} else if len(pins) != 0 {
			t.Fatalf("native pin removal retained parent: %+v", pins)
		}
	}
}
