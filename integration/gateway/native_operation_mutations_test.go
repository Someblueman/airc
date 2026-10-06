//go:build darwin || linux

package gateway_test

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func exerciseNativeMutations(t *testing.T, call nativeOperationCall, accepted nativeAcceptedCall, f nativeOperationFixture, label string, sent core.Result) {
	t.Helper()
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	// Profile/presence semantics belong to shared backend card acceptance.
	// Topic semantics belong to shared backend acceptance.
	// Room-update semantics belong to shared backend acceptance.
	// Pin add/remove semantics belong to shared backend acceptance.
	// Activity semantics belong to shared backend acceptance.
	// Seed native preparation to retain this carrier's nonempty waiting read.
	// Set/cancel semantics and broadcasts belong to shared backend acceptance.
	setReplySignalNative(t, f.config.Bindings[0].Address, sent.MessageID, &core.ReplySignalInput{Note: label, TTLSeconds: 5})
	waitingRequest := core.Request{Version: 1, RequestID: label + "-waiting-active", Operation: core.WaitingRead, Target: room, ParentID: sent.MessageID}
	waitingResult := call(waitingRequest, true).Result
	assertNativeReadFacts(t, f, waitingRequest, waitingResult)
	if waitingResult.Waiting == nil || len(waitingResult.Waiting.Signals) != 1 {
		t.Fatalf("%s active waiting read lost its seeded preparation: %+v", label, waitingResult.Waiting)
	}

	poll := accepted(core.Request{Version: 1, RequestID: label + "-poll-create", Operation: core.PollCreate, Target: room, Poll: &core.PollInput{Question: label + " poll", Options: []string{"yes", "no"}, TTLSeconds: 60}}, true)
	if poll.Poll == nil || poll.Poll.Poll.ID == "" {
		t.Fatalf("%s poll.create=%+v", label, poll)
	}
	pollID := poll.Poll.Poll.ID
	if accepted(core.Request{Version: 1, RequestID: label + "-poll-vote", Operation: core.PollVote, Target: room, PollID: pollID, Choice: 1}, true).State != core.Accepted {
		t.Fatalf("%s poll.vote rejected", label)
	}
	results := call(core.Request{Version: 1, RequestID: label + "-poll-results", Operation: core.PollResults, Target: room, PollID: pollID}, true)
	if results.Result == nil || results.Result.Poll == nil || results.Result.Poll.ID != pollID || len(results.Result.Poll.Votes) != 2 || results.Result.Poll.Votes[0] != 1 {
		t.Fatalf("%s poll.results response=%+v detached=%+v poll=%+v", label, results, results.Result, results.Result.Poll)
	}
	if accepted(core.Request{Version: 1, RequestID: label + "-poll-close", Operation: core.PollClose, Target: room, PollID: pollID}, true).State != core.Accepted {
		t.Fatalf("%s poll.close rejected", label)
	}
	nativePoll := nativePollEntry(t, f.config.Bindings[0].Address, pollID)
	if nativePoll.ID != pollID || !nativePoll.Closed || len(nativePoll.Votes) != 2 || nativePoll.Votes[0] != 1 {
		t.Fatalf("%s native poll=%+v", label, nativePoll)
	}
	// Action/correction/retraction semantics are verified by shared backend acceptance.
}
