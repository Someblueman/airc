//go:build darwin || linux

package gateway_test

import (
	"reflect"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func exerciseCarrierSeededReads(t *testing.T, call nativeOperationCall, f nativeOperationFixture, label string, sent core.Result) {
	t.Helper()
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	// Seed native preparation to retain this carrier's nonempty waiting read.
	// Set/cancel semantics and broadcasts belong to shared backend acceptance.
	setReplySignalNative(t, f.config.Upstream.Address, sent.MessageID, &core.ReplySignalInput{Note: label, TTLSeconds: 5})
	waitingRequest := core.Request{Version: 1, RequestID: label + "-waiting-active", Operation: core.WaitingRead, Target: room, ParentID: sent.MessageID}
	waitingResult := call(waitingRequest, true).Result
	assertNativeReadFacts(t, f, waitingRequest, waitingResult)
	if waitingResult.Waiting == nil || len(waitingResult.Waiting.Signals) != 1 {
		t.Fatalf("%s active waiting read lost its seeded preparation: %+v", label, waitingResult.Waiting)
	}

	_, pollID := seedPollOnlyAt(t, f.config.Upstream.Address, 60)
	pollNativeVote(t, f.config.Upstream.Address, pollID, 1)
	results := call(core.Request{Version: 1, RequestID: label + "-poll-results", Operation: core.PollResults, Target: room, PollID: pollID}, true)
	native := nativePollEntry(t, f.config.Upstream.Address, pollID)
	want := core.PollResult{Target: *room, ID: native.ID, From: native.From, AccountID: native.AccountID, Question: native.Text, Options: native.Options, Votes: native.Votes, ExpiresAt: native.ExpiresAt, Closed: native.Closed}
	if results.Result == nil || results.Result.Poll == nil || !reflect.DeepEqual(*results.Result.Poll, want) || want.Closed || len(want.Votes) != 2 || want.Votes[0] != 1 {
		t.Fatalf("%s poll.results response=%+v native=%+v", label, results, want)
	}
}
