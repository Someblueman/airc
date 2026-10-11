//go:build darwin || linux

package gateway_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestPinMutationProvesExactRoomBeforeNativeWrite(t *testing.T) {
	f := newReactionHTTPFixture(t)
	foreign := seedPinMutationMessage(t, f.socket, "#other-private", "foreign-pin-parent")
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	request := core.Request{Version: 1, RequestID: "pin-forged-add", Operation: core.PinsAdd, Target: target, ParentID: foreign}
	roomHeaderPost(t, f, request, 1)
	blocked := waitHTTPPinResult(t, f, request.RequestID, core.Blocked)
	if blocked.Pin != nil || blocked.Accepted == nil || *blocked.Accepted || blocked.AcceptanceUncertain || blocked.Failure == nil || blocked.Failure.Code != core.Forbidden {
		t.Fatalf("foreign ID add was not blocked before writing: %+v", blocked)
	}
	if pins := queryPinMutationNative(t, f.socket, "#other-private"); len(pins) != 0 {
		t.Fatalf("forged add mutated foreign room: %v", pins)
	}
	seedNativePin(t, f, foreign)
	request.RequestID, request.Operation = "pin-forged-remove", core.PinsRemove
	roomHeaderPost(t, f, request, 1)
	blocked = waitHTTPPinResult(t, f, request.RequestID, core.Blocked)
	if blocked.Pin != nil || blocked.Accepted == nil || *blocked.Accepted || blocked.AcceptanceUncertain || blocked.Failure == nil || blocked.Failure.Code != core.Forbidden {
		t.Fatalf("foreign ID remove was not blocked before writing: %+v", blocked)
	}
	if pins := queryPinMutationNative(t, f.socket, "#other-private"); len(pins) != 1 || pins[0].ID != foreign {
		t.Fatalf("forged remove changed foreign pin: %v", pins)
	}
	request.RequestID, request.Target = "pin-unauthorized-room", &core.Target{Kind: "room", Name: "#other-private"}
	status, _, _ := headerReactionHTTP(t, f, http.MethodPost, "/v1/messages", requestJSON(t, request))
	if status != http.StatusForbidden {
		t.Fatalf("unauthorized room admission: %d", status)
	}
	if status, _ := headerRead(t, f, "/v1/requests/"+request.RequestID); status != http.StatusNotFound {
		t.Fatalf("unauthorized room created job: %d", status)
	}
}

func TestPinMutationRemoveAfterEvictionAndRetainedNoOp(t *testing.T) {
	f := newReactionHTTPFixture(t)
	parent := seedPinMutationMessage(t, f.socket, "#gateway-fixture", "pin-evicted-parent")
	request := core.Request{Version: 1, RequestID: "pin-before-eviction", Operation: core.PinsAdd, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, ParentID: parent}
	roomHeaderPost(t, f, request, 1)
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
	roomHeaderPost(t, f, request, 1)
	assertPinMutationFacts(t, waitHTTPPinResult(t, f, request.RequestID, core.Accepted), request)
	if pins := queryPinMutationNative(t, f.socket, request.Target.Name); len(pins) != 0 {
		t.Fatalf("evicted pin not removed: %v", pins)
	}
	request.RequestID, request.Operation = "pin-add-evicted", core.PinsAdd
	roomHeaderPost(t, f, request, 1)
	blocked := waitHTTPPinResult(t, f, request.RequestID, core.Blocked)
	if blocked.Pin != nil || blocked.AcceptanceUncertain || blocked.Accepted == nil || *blocked.Accepted {
		t.Fatalf("evicted add lacked closed proof: %+v", blocked)
	}
	request.ParentID = seedPinMutationMessage(t, f.socket, request.Target.Name, "pin-retained-noop")
	request.RequestID, request.Operation = "pin-remove-retained-noop", core.PinsRemove
	roomHeaderPost(t, f, request, 1)
	assertPinMutationFacts(t, waitHTTPPinResult(t, f, request.RequestID, core.Accepted), request)
	if pins := queryPinMutationNative(t, f.socket, request.Target.Name); len(pins) != 0 {
		t.Fatalf("no-op unpin created pin: %v", pins)
	}
}

func TestPinMutationRejectsInvalidIntentBeforeAdmission(t *testing.T) {
	f := newReactionHTTPFixture(t)
	for _, operation := range []core.Operation{core.PinsAdd, core.PinsRemove} {
		original := core.Request{Version: 1, Operation: operation, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, ParentID: strings.Repeat("a", 32)}
		for name, mutate := range map[string]func(*core.Request){
			"missing-parent": func(r *core.Request) { r.ParentID = "" },
			"bad-parent":     func(r *core.Request) { r.ParentID = "invalid" },
			"missing-target": func(r *core.Request) { r.Target = nil },
			"direct":         func(r *core.Request) { r.Target = &core.Target{Kind: "direct", Name: "fixture-author"} },
			"message":        func(r *core.Request) { r.Message = "body" },
			"reaction":       func(r *core.Request) { r.Reaction = "agree" },
			"poll":           func(r *core.Request) { r.PollID = strings.Repeat("b", 32) },
			"topic":          func(r *core.Request) { s := "topic"; r.Topic = &s },
			"room":           func(r *core.Request) { n := int64(0); r.Room = &core.RoomPatch{SlowSeconds: &n} },
			"profile":        func(r *core.Request) { r.Profile = &core.ProfilePatch{Clear: true} },
			"presence":       func(r *core.Request) { r.Presence = &core.PresenceInput{State: "away", TTLSeconds: 1} },
			"cursor":         func(r *core.Request) { r.Cursor = "cursor" },
			"limit":          func(r *core.Request) { r.Limit = 1 },
			"query":          func(r *core.Request) { r.Query = "query" },
			"thread":         func(r *core.Request) { r.ThreadMode = "thread" },
		} {
			request := original
			request.RequestID = strings.ReplaceAll(string(operation), ".", "-") + "-bad-" + name
			mutate(&request)
			status, _, _ := headerReactionHTTP(t, f, http.MethodPost, "/v1/messages", requestJSON(t, request))
			if status != http.StatusBadRequest {
				t.Fatalf("%s %s accepted invalid intent: %d", operation, name, status)
			}
			if status, _ := headerRead(t, f, "/v1/requests/"+request.RequestID); status != http.StatusNotFound {
				t.Fatalf("%s %s created job: %d", operation, name, status)
			}
		}
	}
}
