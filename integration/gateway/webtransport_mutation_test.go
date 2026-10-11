//go:build darwin || linux

package gateway_test

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestWebTransportAllMutationOperationsAndNativeReplyFacts(t *testing.T) {
	f := newWebTransportFixture(t)
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	send := core.Request{Version: 1, RequestID: "all-send", Operation: core.Send, Target: room, Message: "WebTransport send whole body 🙂"}
	sendResult := webTransportAccepted(t, f, send, true)
	if sendResult.MessageID == "" {
		t.Fatal("WebTransport send had no durable message ID")
	}
	nativeSend := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == sendResult.MessageID })
	if nativeSend.Message != send.Message || nativeSend.Target != room.Name || nativeSend.ReplyTo != "" || nativeSend.Reaction != "" {
		t.Fatalf("WebTransport native send facts=%+v", nativeSend)
	}
	if reply := webTransportAccepted(t, f, core.Request{Version: 1, RequestID: "all-reply", Operation: core.Reply, Target: room, ParentID: sendResult.MessageID, Message: "WebTransport reply"}, true); reply.MessageID == "" {
		t.Fatal("WebTransport reply had no durable message ID")
	}
	nativeReply := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == "WebTransport reply" })
	if nativeReply.ReplyTo != sendResult.MessageID || nativeReply.ThreadID != sendResult.MessageID || nativeReply.Target != room.Name {
		t.Fatalf("WebTransport native reply facts=%+v", nativeReply)
	}
	for _, request := range []core.Request{
		{Version: 1, RequestID: "all-reaction", Operation: core.ReactionAdd, Target: room, ParentID: sendResult.MessageID, Reaction: "agree"},
		{Version: 1, RequestID: "all-profile", Operation: core.ProfileUpdate, Profile: &core.ProfilePatch{About: strptr("WebTransport profile")}},
		{Version: 1, RequestID: "all-presence", Operation: core.PresenceSet, Presence: &core.PresenceInput{State: "away", Note: "WebTransport", TTLSeconds: 30}},
		{Version: 1, RequestID: "all-topic", Operation: core.TopicSet, Target: room, Topic: strptr("WebTransport topic")},
		{Version: 1, RequestID: "all-room", Operation: core.RoomUpdate, Target: room, Room: &core.RoomPatch{SlowSeconds: webTransportInt64Ptr(1), HistoryLimit: webTransportIntPtr(32)}},
		{Version: 1, RequestID: "all-pin-add", Operation: core.PinsAdd, Target: room, ParentID: sendResult.MessageID},
		{Version: 1, RequestID: "all-pin-remove", Operation: core.PinsRemove, Target: room, ParentID: sendResult.MessageID},
		{Version: 1, RequestID: "all-activity", Operation: core.ActivitySet, Target: room, Activity: &core.ActivityInput{Kind: "typing", Note: "WebTransport", TTLSeconds: 5}},
		{Version: 1, RequestID: "all-signal-set", Operation: core.ReplySignalSet, Target: room, ParentID: sendResult.MessageID, ReplySignal: &core.ReplySignalInput{Note: "WebTransport", TTLSeconds: 5}},
		{Version: 1, RequestID: "all-signal-cancel", Operation: core.ReplySignalCancel, Target: room, ParentID: sendResult.MessageID},
	} {
		result := webTransportAccepted(t, f, request, true)
		switch request.Operation {
		case core.ReactionAdd:
			nativeReaction := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Reaction == request.Reaction && e.ReplyTo == request.ParentID })
			if nativeReaction.Target != room.Name || nativeReaction.Message != request.Reaction {
				t.Fatalf("WebTransport reaction native=%+v", nativeReaction)
			}
		case core.TopicSet:
			if result.Topic == nil || result.Topic.Topic != queryNativeTopic(t, f.config.Upstream.Address, room.Name) {
				t.Fatalf("WebTransport topic ACK/native mismatch=%+v", result.Topic)
			}
		case core.RoomUpdate:
			if result.Room == nil || result.Room.Room != queryNativeRoom(t, f.config.Upstream.Address, room.Name) {
				t.Fatalf("WebTransport room ACK/native mismatch=%+v", result.Room)
			}
		case core.PinsAdd:
			if pins := queryPinMutationNative(t, f.config.Upstream.Address, room.Name); len(pins) != 1 || pins[0].ID != sendResult.MessageID {
				t.Fatalf("WebTransport pin.add native=%+v", pins)
			}
		case core.PinsRemove:
			if pins := queryPinMutationNative(t, f.config.Upstream.Address, room.Name); len(pins) != 0 {
				t.Fatalf("WebTransport pin.remove native=%+v", pins)
			}
		case core.ActivitySet:
			assertActivityMutationFacts(t, result, request, nextActivitySignal(t, f.observer))
		case core.ReplySignalSet, core.ReplySignalCancel:
			assertReplySignalFacts(t, result, request, nextReplySignal(t, f.observer, sendResult.MessageID))
		}
	}
	directory := webTransportBidiCall(t, f, core.Request{Version: 1, RequestID: "all-directory", Operation: core.Directory, Query: "fixture-author", Limit: 8}, true)
	if directory.Directory == nil || len(directory.Directory.Agents) != 1 || directory.Directory.Agents[0].Nick != "fixture-author" {
		t.Fatalf("WebTransport directory missing card=%+v", directory.Directory)
	}
	card := directory.Directory.Agents[0]
	if card.About != "WebTransport profile" || card.State != "away" || card.Note != "WebTransport" || card.ExpiresAt.IsZero() {
		t.Fatalf("WebTransport profile/presence directory=%+v", card)
	}
	pollResult := webTransportAccepted(t, f, core.Request{Version: 1, RequestID: "all-poll-create", Operation: core.PollCreate, Target: room, Poll: &core.PollInput{Question: "WebTransport poll", Options: []string{"yes", "no"}, TTLSeconds: 60}}, true)
	if pollResult.Poll == nil || pollResult.Poll.Poll.ID == "" {
		t.Fatalf("WebTransport poll.create=%+v", pollResult)
	}
	pollID := pollResult.Poll.Poll.ID
	webTransportAccepted(t, f, core.Request{Version: 1, RequestID: "all-poll-vote", Operation: core.PollVote, Target: room, PollID: pollID, Choice: 1}, true)
	results := webTransportBidiCall(t, f, core.Request{Version: 1, RequestID: "all-poll-results", Operation: core.PollResults, Target: room, PollID: pollID}, true)
	if results.Poll == nil || results.Poll.ID != pollID || len(results.Poll.Votes) != 2 || results.Poll.Votes[0] != 1 {
		t.Fatalf("WebTransport poll.results=%+v", results.Poll)
	}
	closed := webTransportAccepted(t, f, core.Request{Version: 1, RequestID: "all-poll-close", Operation: core.PollClose, Target: room, PollID: pollID}, true)
	if closed.Poll == nil || !closed.Poll.Poll.Closed {
		t.Fatalf("WebTransport poll.close ACK=%+v", closed.Poll)
	}
	action := webTransportAccepted(t, f, core.Request{Version: 1, RequestID: "all-action", Operation: core.MessageAction, Target: room, Message: "WebTransport action"}, true)
	corrected := webTransportAccepted(t, f, core.Request{Version: 1, RequestID: "all-correct", Operation: core.MessageCorrect, Target: room, ParentID: action.MessageID, Message: "WebTransport corrected"}, true)
	retracted := webTransportAccepted(t, f, core.Request{Version: 1, RequestID: "all-retract", Operation: core.MessageRetract, Target: room, ParentID: action.MessageID}, true)
	history := webTransportBidiCall(t, f, core.Request{Version: 1, RequestID: "all-history", Operation: core.HistoryRead, Target: room, Start: "recent", Limit: 64}, true)
	if history.Page == nil {
		t.Fatal("WebTransport action history missing")
	}
	var foundAction, foundCorrection bool
	for _, message := range history.Page.Messages {
		if message.ID == action.MessageID && message.Retracted && message.SupersededBy == retracted.MessageID {
			foundAction = true
		}
		if message.ID == corrected.MessageID && message.Message == "WebTransport corrected" {
			foundCorrection = true
		}
	}
	if !foundAction || !foundCorrection {
		t.Fatalf("WebTransport action annotations missing: action=%s correction=%s retract=%s", action.MessageID, corrected.MessageID, retracted.MessageID)
	}
}

func webTransportIntPtr(value int) *int       { return &value }
func webTransportInt64Ptr(value int64) *int64 { return &value }
