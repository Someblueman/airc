//go:build darwin || linux

package gateway_test

import (
	"crypto/rand"
	"reflect"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestUnixStreamExecutesRoomContextPinsWaitingAndReaction(t *testing.T) {
	conn, credentials, stop, _, upstream := unixStreamFixture(t)
	defer conn.Close()
	observer := dial(t, upstream, "unix-observer", "", false, false)
	defer observer.Close()
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "unix-observer" })
	call := func(request core.Request) dispatch.DetachedResult {
		t.Helper()
		correlation := rand.Text()
		writeFrame(t, conn, frameRequest{Version: 1, CorrelationID: correlation, Profile: "plain", Request: credentials.token(t, request, true)})
		response := readFrame(t, conn)
		if response.CorrelationID != correlation || response.Error != nil || response.Result == nil || response.Result.Operation != request.Operation {
			t.Fatalf("Unix %s failed: %+v", request.Operation, response)
		}
		return *response.Result
	}
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	send := core.Request{Version: 1, RequestID: "unix-parent", Operation: core.Send, Target: target, Message: "whole Unix parent\nUnicode: café ☃"}
	call(send)
	parentID := unixAccepted(t, call, send.RequestID)
	parent := next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == parentID })
	if parent.Message != send.Message || parent.Target != target.Name || parent.From != "fixture-author" || parent.AccountID == "" {
		t.Fatalf("native parent mismatch: %+v", parent)
	}
	reply := core.Request{Version: 1, RequestID: "unix-reply", Operation: core.Reply, Target: target, ParentID: parentID, Message: "whole Unix reply"}
	call(reply)
	replyID := unixAccepted(t, call, reply.RequestID)
	child := next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == replyID })
	if child.Message != reply.Message || child.Target != target.Name || child.ReplyTo != parentID || child.ThreadID != parentID || child.AccountID != parent.AccountID {
		t.Fatalf("native reply mismatch: %+v", child)
	}
	reaction := core.Request{Version: 1, RequestID: "unix-reaction", Operation: core.ReactionAdd, Target: target, ParentID: parentID, Reaction: "seen"}
	call(reaction)
	reactionID := unixAccepted(t, call, reaction.RequestID)
	seen := next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == reactionID })
	if seen.Reaction != "seen" || seen.Message != "seen" || seen.Target != target.Name || seen.ReplyTo != parentID || seen.AccountID != parent.AccountID {
		t.Fatalf("native reaction mismatch: %+v", seen)
	}
	author := dial(t, upstream, "fixture-author", fixtureAccount, false, true)
	defer author.Close()
	if err := author.Chat(irc.ChatRequest{Action: "pin", Target: target.Name, ID: parentID}); err != nil {
		t.Fatal(err)
	}
	next(t, author, func(e *irc.ChatEvent) bool { return e.Action == "pin" && e.ID == parentID })
	next(t, author, func(e *irc.EndOfChatEvent) bool { return true })
	if err := author.Chat(irc.ChatRequest{Action: "pins", Target: target.Name}); err != nil {
		t.Fatal(err)
	}
	pin := next(t, author, func(e *irc.ChatEvent) bool { return e.Action == "pin" && e.ID == parentID })
	next(t, author, func(e *irc.EndOfChatEvent) bool { return true })
	if pin.Message == nil {
		t.Fatal("native pin omitted its whole message")
	}
	if err := author.Chat(irc.ChatRequest{Action: "prepare", Target: target.Name, ID: parentID, Seconds: 60, Text: "reply coming over Unix"}); err != nil {
		t.Fatal(err)
	}
	prepared := next(t, author, func(e *irc.ChatEvent) bool { return e.Action == "prepare" && e.ID == parentID })
	next(t, author, func(e *irc.EndOfChatEvent) bool { return true })
	context := call(core.Request{Version: 1, RequestID: "unix-context", Operation: core.ContextRead, Target: target, ParentID: parentID, Limit: 10}).Context
	if context == nil || context.Summary.TriggerID != parentID || context.Summary.RootID != parentID || len(context.Messages) != 3 {
		t.Fatalf("context mismatch: %+v", context)
	}
	byID := make(map[string]irc.MessageMetadata)
	for _, message := range context.Messages {
		byID[message.ID] = message
	}
	if byID[parentID].Message != send.Message || byID[replyID].ReplyTo != parentID || byID[reactionID].Reaction != "seen" {
		t.Fatalf("context lost whole records: %+v", context)
	}
	pins := call(core.Request{Version: 1, RequestID: "unix-pins", Operation: core.PinsRead, Target: target}).Pins
	if pins == nil || pins.Target != *target || len(pins.Pins) != 1 || !reflect.DeepEqual(pins.Pins[0], *pin.Message) {
		t.Fatalf("whole pins mismatch: %+v", pins)
	}
	room := call(core.Request{Version: 1, RequestID: "unix-room", Operation: core.RoomRead, Target: target}).Room
	if room == nil || room.Target != *target || room.SlowSeconds != 0 || room.HistoryLimit != 0 {
		t.Fatalf("stored room settings mismatch: %+v", room)
	}
	waiting := call(core.Request{Version: 1, RequestID: "unix-waiting", Operation: core.WaitingRead, Target: target, ParentID: parentID}).Waiting
	if waiting == nil || waiting.Target != *target || waiting.ParentID != parentID || len(waiting.Signals) != 1 || !reflect.DeepEqual(waiting.Signals[0], prepared.ChatEntry) {
		t.Fatalf("whole waiting signal mismatch: %+v", waiting)
	}
	stop()
}

func unixAccepted(t *testing.T, call func(core.Request) dispatch.DetachedResult, requestID string) string {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		result := call(core.Request{Version: 1, RequestID: "receipt-" + requestID, Operation: core.ReceiptGet, LookupID: requestID}).Result
		if result != nil && result.State == core.Accepted && result.MessageID != "" {
			return result.MessageID
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("Unix mutation was not accepted", requestID)
	return ""
}
