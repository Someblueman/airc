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
	pin := core.Request{Version: 1, RequestID: label + "-pin-add", Operation: core.PinsAdd, Target: room, ParentID: sent.MessageID}
	accepted(pin, true)
	if pins := queryPinMutationNative(t, f.config.Bindings[0].Address, room.Name); len(pins) != 1 || pins[0].ID != sent.MessageID {
		t.Fatalf("%s native pin.add=%+v", label, pins)
	}
	pin.RequestID, pin.Operation = label+"-pin-remove", core.PinsRemove
	accepted(pin, true)
	if pins := queryPinMutationNative(t, f.config.Bindings[0].Address, room.Name); len(pins) != 0 {
		t.Fatalf("%s native pin.remove=%+v", label, pins)
	}
	activity := core.Request{Version: 1, RequestID: label + "-activity", Operation: core.ActivitySet, Target: room, Activity: &core.ActivityInput{Kind: "typing", Note: label, TTLSeconds: 5}}
	assertActivityMutationFacts(t, accepted(activity, true), activity, nextActivitySignal(t, f.observer))
	signal := core.Request{Version: 1, RequestID: label + "-signal-set", Operation: core.ReplySignalSet, Target: room, ParentID: sent.MessageID, ReplySignal: &core.ReplySignalInput{Note: label, TTLSeconds: 5}}
	assertReplySignalFacts(t, accepted(signal, true), signal, nextReplySignal(t, f.observer, sent.MessageID))
	waitingRequest := core.Request{Version: 1, RequestID: label + "-waiting-active", Operation: core.WaitingRead, Target: room, ParentID: sent.MessageID}
	assertNativeReadFacts(t, f, waitingRequest, call(waitingRequest, true).Result)
	signal.RequestID, signal.Operation, signal.ReplySignal = label+"-signal-cancel", core.ReplySignalCancel, nil
	assertReplySignalFacts(t, accepted(signal, true), signal, nextReplySignal(t, f.observer, sent.MessageID))
	if waiting := queryNativeWaitingSignal(t, f.config.Bindings[0].Address, sent.MessageID); len(waiting) != 0 {
		t.Fatalf("%s reply signal cancel left native waiting=%+v", label, waiting)
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
