//go:build darwin || linux

package gateway_test

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestPollMutationForeignRoomAndHistoryEviction(t *testing.T) {
	f := newReactionTransportFixture(t)
	defer f.framed.Close()
	defer f.ws.CloseNow()
	defer f.watch.CloseNow()
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	before, foreignID := seedForeignPoll(t, f)
	for _, op := range []core.Operation{core.PollVote, core.PollClose} {
		r := pollMutationRequest("poll-forged-"+strings.ReplaceAll(string(op), ".", "-"), op, target, foreignID)
		roomBearerPost(t, f, r, 1)
		blocked := waitHTTPPinResult(t, f, r.RequestID, core.Blocked)
		if blocked.Poll != nil || blocked.Accepted == nil || *blocked.Accepted || blocked.AcceptanceUncertain || blocked.Failure == nil || blocked.Failure.Code != core.Forbidden {
			t.Fatalf("foreign poll allowed mutation: %+v", blocked)
		}
		after := nativePollEntry(t, f.socket, foreignID)
		if after.Closed != before.Closed || !reflect.DeepEqual(after.Votes, before.Votes) {
			t.Fatal("foreign poll was modified", before, after)
		}
	}
	_, id := seedPollOnlyAt(t, f.socket, 120)
	evictPollHistory(t, f.socket, id)
	observer := newReplySignalObserver(t, f.socket, *target)
	for _, op := range []core.Operation{core.PollVote, core.PollClose} {
		r := pollMutationRequest("poll-evicted-"+strings.ReplaceAll(string(op), ".", "-"), op, target, id)
		roomBearerPost(t, f, r, 1)
		signal := nextPollMutationSignal(t, observer, id)
		assertPollMutationFacts(t, waitHTTPPinResult(t, f, r.RequestID, core.Accepted), r, &signal.ChatEntry)
	}
	// A closed poll rejects new voting without changing the saved aggregate.
	beforeClosed := nativePollEntry(t, f.socket, id)
	r := pollMutationRequest("poll-after-close", core.PollVote, target, id)
	roomBearerPost(t, f, r, 1)
	blocked := waitHTTPPinResult(t, f, r.RequestID, core.Rejected)
	if blocked.Poll != nil || blocked.AcceptanceUncertain || blocked.Failure == nil || blocked.Failure.Code != core.InvalidRequest {
		t.Fatalf("closed poll vote result: %+v", blocked)
	}
	if after := nativePollEntry(t, f.socket, id); !reflect.DeepEqual(after, beforeClosed) {
		t.Fatal("closed poll vote changed native state", beforeClosed, after)
	}
	assertNoReplySignal(t, observer)
}

func TestPollMutationInvalidIntentCreatesNoJob(t *testing.T) {
	f := newReactionTransportFixture(t)
	defer f.framed.Close()
	defer f.ws.CloseNow()
	defer f.watch.CloseNow()
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	for _, op := range []core.Operation{core.PollCreate, core.PollVote, core.PollClose} {
		for name, mutate := range map[string]func(*core.Request){
			"message": func(r *core.Request) { r.Message = "body" },
			"parent":  func(r *core.Request) { r.ParentID = strings.Repeat("a", 32) },
			"direct":  func(r *core.Request) { r.Target = &core.Target{Kind: "direct", Name: "fixture-peer"} },
			"wrong-input": func(r *core.Request) {
				if op == core.PollCreate {
					r.Poll = nil
				} else {
					r.Poll = &core.PollInput{Question: "foreign", Options: []string{"a", "b"}, TTLSeconds: 1}
				}
			},
		} {
			id := strings.Repeat("a", 32)
			if op == core.PollCreate {
				id = ""
			}
			r := pollMutationRequest("poll-invalid-"+strings.ReplaceAll(string(op), ".", "-")+"-"+name, op, target, id)
			mutate(&r)
			status, _, _ := bearerReactionHTTP(t, f, http.MethodPost, "/v1/messages", requestJSON(t, r))
			if status != http.StatusBadRequest {
				t.Fatalf("invalid %s %s status=%d", op, name, status)
			}
			if status, _ := bearerRead(t, f, "/v1/requests/"+r.RequestID); status != http.StatusNotFound {
				t.Fatalf("invalid poll created job: %d", status)
			}
		}
	}
}
