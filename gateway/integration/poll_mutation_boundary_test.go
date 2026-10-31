//go:build darwin || linux

package gateway_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/core"
)

func TestPollMutationForeignRoomAndHistoryEviction(t *testing.T) {
	t.Parallel()
	f := newReactionHTTPFixture(t)
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	before, foreignID := seedForeignPoll(t, f)
	for _, op := range []core.Operation{core.PollVote, core.PollClose} {
		r := pollMutationRequest("poll-forged-"+strings.ReplaceAll(string(op), ".", "-"), op, target, foreignID)
		headerPost(t, f, r)
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
		headerPost(t, f, r)
		signal := nextPollMutationSignal(t, observer, id)
		assertPollMutationFacts(t, waitHTTPPinResult(t, f, r.RequestID, core.Accepted), r, &signal.ChatEntry)
	}
	// A closed poll rejects new voting without changing the saved aggregate.
	beforeClosed := nativePollEntry(t, f.socket, id)
	r := pollMutationRequest("poll-after-close", core.PollVote, target, id)
	headerPost(t, f, r)
	blocked := waitHTTPPinResult(t, f, r.RequestID, core.Rejected)
	if blocked.Poll != nil || blocked.AcceptanceUncertain || blocked.Failure == nil || blocked.Failure.Code != core.InvalidRequest {
		t.Fatalf("closed poll vote result: %+v", blocked)
	}
	if after := nativePollEntry(t, f.socket, id); !reflect.DeepEqual(after, beforeClosed) {
		t.Fatal("closed poll vote changed native state", beforeClosed, after)
	}
	assertNoReplySignal(t, observer)
}
