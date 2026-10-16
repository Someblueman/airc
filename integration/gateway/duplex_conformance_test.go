//go:build darwin || linux

package gateway_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// This exercises real standalone transports, not dispatcher mocks.
func duplexOperations(t *testing.T, credentials *detachedCredentials, secure bool, watchMode string, _ int64, write func(frameRequest), read func() adapter.FrameResponse, stop func()) {
	t.Helper()
	profile := "encrypted"
	if secure {
		profile = "signed"
	}
	sequence := 0
	tokens := map[string]string{}
	call := func(id string, r core.Request) adapter.FrameResponse {
		sequence++
		id += fmt.Sprint("-", sequence)
		token := credentials.token(t, r, profile == "encrypted")
		if r.Operation == core.Send || r.Operation == core.Reply || r.Operation == core.ReactionAdd {
			if saved := tokens[r.RequestID]; saved != "" {
				token = saved
			} else {
				tokens[r.RequestID] = token
			}
		}
		write(frameRequest{Version: 1, CorrelationID: id, Profile: profile, Request: token})
		response := read()
		if response.CorrelationID != id || response.Event != "result" || response.Error != nil || response.Result == nil {
			t.Fatal("operation failed", response)
		}
		return response
	}
	discovery := call("discover", core.Request{Version: 1, RequestID: "discover", Operation: core.CapabilitiesGet})
	caps := discovery.Result.Capabilities
	expected := []core.Operation{core.Send, core.Reply, core.ReactionAdd, core.ContextRead, core.PinsRead, core.RoomRead, core.WaitingRead, core.PollResults, core.TopicRead, core.MembersRead, core.ReceiptGet, core.HistoryRead, core.Check, core.Search, core.Watch, core.EventsWatch, core.Directory, core.ChannelsRead, core.ProfileUpdate, core.PresenceSet, core.TopicSet, core.RoomUpdate, core.PinsAdd, core.PinsRemove, core.ActivitySet, core.ReplySignalSet, core.ReplySignalCancel, core.PollCreate, core.PollVote, core.PollClose, core.MessageAction, core.MessageCorrect, core.MessageRetract, core.AgentsRead}
	// A listener that admits any target publishes no per-operation target
	// list; one with an allowlist publishes its rooms.
	roomTarget := []core.Target{{Kind: "room", Name: "#gateway-fixture"}}
	if caps == nil || !slices.Equal(caps.Operations, expected) || strings.Join(caps.WatchModes, ",") != "long_poll,"+watchMode || caps.MaxPins != 5 || caps.MaxPinsBytes != 65536 || len(caps.PinsTargets) != 0 && !slices.Equal(caps.PinsTargets, roomTarget) || caps.MaxRoomBytes != core.MaxRoomBytes || caps.MaxWaitingSignals != core.MaxWaitingSignals || caps.MaxWaitingBytes != core.MaxWaitingBytes || caps.MaxPollBytes != core.MaxPollBytes || caps.MaxTopicBytes != core.MaxTopicBytes || caps.MaxCheckRooms != core.MaxCheckRooms || caps.MaxCheckWaitSeconds != core.MaxCheckWaitSeconds {
		t.Fatal("wrong framed capabilities", caps)
	}
	// One check reads the room from its oldest retained message, and the
	// registered author's inbox.
	checked := call("check", core.Request{Version: 1, RequestID: "check", Operation: core.Check, Limit: 5, Rooms: []core.CheckRoom{{Name: "#gateway-fixture", After: "retained"}}, Inbox: &core.CheckInbox{}})
	if c := checked.Result.Check; c == nil || len(c.Rooms) != 1 || c.Rooms[0].Room != "#gateway-fixture" || c.Rooms[0].Status != "ok" && c.Rooms[0].Status != "more" || c.Inbox == nil || c.Inbox.Room != "@fixture-author" {
		t.Fatal("check over the duplex carrier", checked.Result)
	}
	if caps.AgentsScope != "public_global" || caps.MaxAgents != core.MaxAgents || caps.MaxAgentsBytes != core.MaxAgentsBytes {
		t.Fatal("live agent capabilities not explicit and bounded", caps)
	}
	if secure {
		write(frameRequest{Version: 1, CorrelationID: "tls-encrypted", Profile: "plain", Request: credentials.token(t, core.Request{Version: 1, RequestID: "tls-encrypted", Operation: core.CapabilitiesGet}, true)})
		if r := read(); r.Result == nil || r.Result.Capabilities == nil {
			t.Fatal("TLS encrypted profile failed", r)
		}
	}
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	send := core.Request{Version: 1, RequestID: "stream-send", Operation: core.Send, Target: target, Message: "TCP/TLS whole body\n🙂"}
	admitted := call("send", send)
	if admitted.Result.Result == nil || !admitted.Result.Result.Durable {
		t.Fatal("not durable", admitted)
	}
	var messageID string
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		r := call("receipt", core.Request{Version: 1, RequestID: "receipt", Operation: core.ReceiptGet, LookupID: send.RequestID})
		if r.Result.Result.State == core.Accepted {
			messageID = r.Result.Result.MessageID
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if messageID == "" {
		t.Fatal("no actual native acceptance")
	}
	replay := call("replay", send)
	if replay.Result.Result.MessageID != messageID || replay.Result.Result.SendAttempts != 1 {
		t.Fatal("replay redispatched", replay)
	}
	reply := send
	reply.RequestID = "stream-reply"
	reply.Operation = core.Reply
	reply.ParentID = messageID
	reply.Message = "framed reply"
	_ = call("reply", reply)
	for deadline := time.Now().Add(5 * time.Second); ; {
		r := call("reply-status", core.Request{Version: 1, RequestID: "reply-status", Operation: core.ReceiptGet, LookupID: reply.RequestID})
		if r.Result.Result.State == core.Accepted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reply not accepted", r)
		}
		time.Sleep(20 * time.Millisecond)
	}
	history := call("history", core.Request{Version: 1, RequestID: "history", Operation: core.HistoryRead, Target: target, Limit: 10, Start: "retained"})
	if history.Result.Page == nil || len(history.Result.Page.Messages) < 1 || history.Result.Page.Messages[0].Message != send.Message || history.Result.Page.Messages[0].From != "fixture-author" {
		t.Fatal("native history lost identity/body", history)
	}
	search := call("search", core.Request{Version: 1, RequestID: "search", Operation: core.Search, Target: target, Limit: 10, Query: "whole body"})
	if search.Result.Page == nil || len(search.Result.Page.Messages) != 1 || search.Result.Page.Messages[0].ID != messageID {
		t.Fatal("search mismatch", search)
	}
	directory := call("directory", core.Request{Version: 1, RequestID: "directory", Operation: core.Directory, Limit: 1, Query: "fixture-author"})
	if directory.Result.Directory == nil {
		t.Fatal("missing public directory")
	}
	watch := core.Request{Version: 1, RequestID: "watch", Operation: core.Watch, Target: target, Limit: 10}
	_ = call("poll", watch)
	// The stream ends when its fixed native read lease does.
	write(frameRequest{Version: 1, CorrelationID: "watch", Profile: profile, Request: credentials.token(t, watch, profile == "encrypted"), Stream: true})
	first := read()
	if first.CorrelationID != "watch" || first.Page == nil {
		t.Fatal("missing stream catchup", first)
	}
	send.RequestID = "stream-live"
	send.Message = "send while watch is active"
	write(frameRequest{Version: 1, CorrelationID: "live", Profile: profile, Request: credentials.token(t, send, profile == "encrypted")})
	live, admission := false, false
	var cursor string
	for !live || !admission {
		response := read()
		if response.CorrelationID == "live" && response.Result != nil {
			admission = response.Result.Result.Durable
		}
		if response.CorrelationID == "watch" && response.Page != nil {
			for _, m := range response.Page.Messages {
				if m.Message == send.Message {
					live = true
					cursor = response.Page.Cursor
				}
			}
		}
		if response.Error != nil || response.Event == "end" {
			t.Fatal("multiplex failed", response)
		}
	}
	if !strings.HasPrefix(cursor, "gc1.") {
		t.Fatal("missing scoped stream cursor", cursor)
	}
	for {
		terminal := read()
		if terminal.Event == "end" {
			if terminal.CorrelationID != "watch" || terminal.Reason != "lease_expired" {
				t.Fatal("watch did not end with its lease", terminal)
			}
			break
		}
		if terminal.CorrelationID != "watch" || terminal.Page == nil {
			t.Fatal("unexpected stream continuation", terminal)
		}
	}
	watch.Cursor = cursor
	write(frameRequest{Version: 1, CorrelationID: "renewed", Profile: profile, Request: credentials.token(t, watch, profile == "encrypted"), Stream: true})
	renewed := read()
	if renewed.CorrelationID != "renewed" || renewed.Page == nil || len(renewed.Page.Messages) != 0 {
		t.Fatalf("renewed cursor replayed delivered message: %+v page=%+v", renewed, renewed.Page)
	}
	// Stop with an active native watch: service must close transport and join leases.
	stop()
}
