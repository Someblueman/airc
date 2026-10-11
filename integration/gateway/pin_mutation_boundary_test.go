//go:build darwin || linux

package gateway_test

import (
	"fmt"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestPinMutationProvesExactRoomBeforeNativeWrite(t *testing.T) {
	f := newReactionHTTPFixture(t)
	foreign := seedPinMutationMessage(t, f.socket, "#other-private", "foreign-pin-parent")
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	request := core.Request{Version: 1, RequestID: "pin-forged-add", Operation: core.PinsAdd, Target: target, ParentID: foreign}
	headerPost(t, f, request)
	blocked := waitHTTPPinResult(t, f, request.RequestID, core.Blocked)
	if blocked.Pin != nil || blocked.Accepted == nil || *blocked.Accepted || blocked.AcceptanceUncertain || blocked.Failure == nil || blocked.Failure.Code != core.Forbidden {
		t.Fatalf("foreign ID add was not blocked before writing: %+v", blocked)
	}
	if pins := queryPinMutationNative(t, f.socket, "#other-private"); len(pins) != 0 {
		t.Fatalf("forged add mutated foreign room: %v", pins)
	}
	seedNativePin(t, f, foreign)
	request.RequestID, request.Operation = "pin-forged-remove", core.PinsRemove
	headerPost(t, f, request)
	blocked = waitHTTPPinResult(t, f, request.RequestID, core.Blocked)
	if blocked.Pin != nil || blocked.Accepted == nil || *blocked.Accepted || blocked.AcceptanceUncertain || blocked.Failure == nil || blocked.Failure.Code != core.Forbidden {
		t.Fatalf("foreign ID remove was not blocked before writing: %+v", blocked)
	}
	if pins := queryPinMutationNative(t, f.socket, "#other-private"); len(pins) != 1 || pins[0].ID != foreign {
		t.Fatalf("forged remove changed foreign pin: %v", pins)
	}
}

func TestPinMutationRemoveAfterEvictionAndRetainedNoOp(t *testing.T) {
	f := newReactionHTTPFixture(t)
	parent := seedPinMutationMessage(t, f.socket, "#gateway-fixture", "pin-evicted-parent")
	request := core.Request{Version: 1, RequestID: "pin-before-eviction", Operation: core.PinsAdd, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, ParentID: parent}
	headerPost(t, f, request)
	assertPinMutationFacts(t, waitHTTPPinResult(t, f, request.RequestID, core.Accepted), request)
	author := dial(t, f.socket, "fixture-author", fixtureAccount, false, true)
	for i := 0; i < 33; i++ {
		id := fmt.Sprintf("pin-eviction-%d", i)
		if err := author.SendWithID(request.Target.Name, id, id); err != nil {
			t.Fatal(err)
		}
		next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == id })
	}
	_ = author.Close()
	if snapshot := topicHistorySnapshot(t, f.socket); len(snapshot) != 32 || snapshot[parent] != "" {
		t.Fatalf("history eviction positive control failed: %v", snapshot)
	}
	if pins := queryPinMutationNative(t, f.socket, request.Target.Name); len(pins) != 1 || pins[0].ID != parent {
		t.Fatalf("stored pin did not survive eviction: %v", pins)
	}
	request.RequestID, request.Operation = "pin-remove-evicted", core.PinsRemove
	headerPost(t, f, request)
	assertPinMutationFacts(t, waitHTTPPinResult(t, f, request.RequestID, core.Accepted), request)
	if pins := queryPinMutationNative(t, f.socket, request.Target.Name); len(pins) != 0 {
		t.Fatalf("evicted pin not removed: %v", pins)
	}
	request.RequestID, request.Operation = "pin-add-evicted", core.PinsAdd
	headerPost(t, f, request)
	blocked := waitHTTPPinResult(t, f, request.RequestID, core.Blocked)
	if blocked.Pin != nil || blocked.AcceptanceUncertain || blocked.Accepted == nil || *blocked.Accepted {
		t.Fatalf("evicted add lacked closed proof: %+v", blocked)
	}
	request.ParentID = seedPinMutationMessage(t, f.socket, request.Target.Name, "pin-retained-noop")
	request.RequestID, request.Operation = "pin-remove-retained-noop", core.PinsRemove
	headerPost(t, f, request)
	assertPinMutationFacts(t, waitHTTPPinResult(t, f, request.RequestID, core.Accepted), request)
	if pins := queryPinMutationNative(t, f.socket, request.Target.Name); len(pins) != 0 {
		t.Fatalf("no-op unpin created pin: %v", pins)
	}
}
