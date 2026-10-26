//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestRPCReadMatrixAndReplyFacts(t *testing.T) {
	f := newRPCHTTPFixture(t, false)
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	parentID := seedPinMutationMessage(t, f.config.Upstream.Address, room.Name, "rpc-matrix-parent")
	author := dial(t, f.config.Upstream.Address, "rpc-matrix-author", "", false, true)
	t.Cleanup(func() { _ = author.Close() })
	call := func(id string, request core.Request) rpcResponse {
		response, data := rpcHTTPSend(t, f, "/v1/rpc", rpcRequestBody(t, id, "airc.request", rpcRequestParams(request)), false)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("RPC %s status=%d body=%s", request.Operation, response.StatusCode, data)
		}
		decoded := decodeRPC(t, data)
		if len(decoded.Error) != 0 || len(decoded.Result) == 0 {
			t.Fatalf("RPC %s error/result=%s/%s", request.Operation, decoded.Error, decoded.Result)
		}
		return decoded
	}
	reads := []core.Request{
		{Version: 1, RequestID: "rpc-history", Operation: core.HistoryRead, Target: room, Start: "retained", Limit: 16},
		{Version: 1, RequestID: "rpc-search", Operation: core.Search, Target: room, Query: "rpc-matrix", Limit: 16},
		{Version: 1, RequestID: "rpc-context", Operation: core.ContextRead, Target: room, ParentID: parentID, Limit: 16},
		{Version: 1, RequestID: "rpc-pins", Operation: core.PinsRead, Target: room},
		{Version: 1, RequestID: "rpc-topic", Operation: core.TopicRead, Target: room},
		{Version: 1, RequestID: "rpc-room", Operation: core.RoomRead, Target: room},
		{Version: 1, RequestID: "rpc-waiting", Operation: core.WaitingRead, Target: room, ParentID: parentID},
		{Version: 1, RequestID: "rpc-members", Operation: core.MembersRead, Target: room},
		{Version: 1, RequestID: "rpc-channels", Operation: core.ChannelsRead},
		{Version: 1, RequestID: "rpc-directory", Operation: core.Directory, Query: "fixture-author", Limit: 16},
	}
	for _, request := range reads {
		result := call(string(request.Operation), request)
		if string(result.Result) == "null" || len(result.Result) < 3 {
			t.Fatalf("RPC %s returned empty snapshot", request.Operation)
		}
	}
	eventsRequest := core.Request{Version: 1, RequestID: "rpc-events-ordinary", Operation: core.EventsWatch, Target: room, Start: "retained", Limit: 16}
	eventsResponse, eventsBody := rpcHTTPSend(t, f, "/v1/rpc", rpcRequestBody(t, eventsRequest.RequestID, "airc.request", rpcRequestParams(eventsRequest)), false)
	if eventsResponse.StatusCode != http.StatusOK {
		t.Fatalf("RPC events.watch status=%d body=%s", eventsResponse.StatusCode, eventsBody)
	}
	eventsRPC := decodeRPC(t, eventsBody)
	var eventsResult map[string]json.RawMessage
	if len(eventsRPC.Error) != 0 || json.Unmarshal(eventsRPC.Result, &eventsResult) != nil || (len(eventsResult["page"]) == 0 && len(eventsResult["messages"]) == 0) {
		t.Fatalf("RPC events.watch ordinary page=%s", eventsRPC.Result)
	}

	send := core.Request{Version: 1, RequestID: "rpc-matrix-send", Operation: core.Send, Target: room, Message: "rpc matrix send"}
	sendResult := call("send", send)
	var admitted core.Result
	if err := json.Unmarshal(sendResult.Result, &admitted); err != nil || !admitted.Durable {
		t.Fatalf("RPC send result=%s err=%v", sendResult.Result, err)
	}
	native := next(t, f.observer, func(event *irc.MessageEvent) bool { return event.Message == send.Message })
	if native.ID == "" || native.Target != room.Name || native.From != "fixture-author" {
		t.Fatalf("RPC native send=%+v", native)
	}
	// The observer receives the native broadcast before the sender receipt
	// can be validated and committed to the gateway journal.
	stored := acceptedReceipt(t, 5*time.Second, 40*time.Millisecond, "the RPC send to be accepted", func() *core.Result {
		receipt := call("receipt", core.Request{Version: 1, RequestID: "rpc-matrix-receipt", Operation: core.ReceiptGet, LookupID: send.RequestID})
		var result core.Result
		if err := json.Unmarshal(receipt.Result, &result); err != nil {
			t.Fatalf("RPC receipt=%s err=%v", receipt.Result, err)
		}
		return &result
	})
	if stored.State != core.Accepted || stored.MessageID != native.ID || stored.SendAttempts != 1 || stored.LookupAttempts != 0 {
		t.Fatalf("RPC receipt=%+v native=%s", stored, native.ID)
	}
	reply := core.Request{Version: 1, RequestID: "rpc-matrix-reply", Operation: core.Reply, Target: room, ParentID: native.ID, Message: "rpc matrix reply"}
	call("reply", reply)
	replyNative := next(t, f.observer, func(event *irc.MessageEvent) bool { return event.Message == reply.Message })
	if replyNative.ReplyTo != native.ID || replyNative.Target != room.Name {
		t.Fatalf("RPC native reply=%+v", replyNative)
	}

	conflict := core.Request{Version: 1, RequestID: send.RequestID, Operation: core.Send, Target: room, Message: "rpc conflicting body"}
	conflictResponse, conflictBody := rpcHTTPSend(t, f, "/v1/rpc", rpcRequestBody(t, "conflict", "airc.request", rpcRequestParams(conflict)), false)
	if conflictResponse.StatusCode != http.StatusOK && conflictResponse.StatusCode != http.StatusConflict && conflictResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("RPC conflict status=%d body=%s", conflictResponse.StatusCode, conflictBody)
	}
	if conflictResponse.StatusCode == http.StatusOK && len(decodeRPC(t, conflictBody).Error) == 0 {
		t.Fatalf("RPC conflict lacked error: %s", conflictBody)
	}
}

func TestRPCAllMutationOperationsReachAccepted(t *testing.T) {
	f := newRPCHTTPFixture(t, false)
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	call := func(request core.Request) core.Result {
		body := rpcRequestBody(t, request.RequestID, "airc.request", rpcRequestParams(request))
		response, data := rpcHTTPSend(t, f, "/v1/rpc", body, false)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("RPC %s status=%d body=%s", request.Operation, response.StatusCode, data)
		}
		decoded := decodeRPC(t, data)
		if len(decoded.Error) != 0 {
			t.Fatalf("RPC %s error=%s", request.Operation, decoded.Error)
		}
		var result core.Result
		if err := json.Unmarshal(decoded.Result, &result); err != nil || !result.Durable {
			t.Fatalf("RPC %s admission=%s err=%v", request.Operation, decoded.Result, err)
		}
		return acceptedReceipt(t, 5*time.Second, 40*time.Millisecond, fmt.Sprintf("RPC %s to reach accepted", request.Operation), func() *core.Result {
			receipt := core.Request{Version: 1, RequestID: request.RequestID + "-receipt", Operation: core.ReceiptGet, LookupID: request.RequestID}
			receiptResponse, receiptBody := rpcHTTPSend(t, f, "/v1/rpc", rpcRequestBody(t, receipt.RequestID, "airc.request", rpcRequestParams(receipt)), false)
			if receiptResponse.StatusCode != http.StatusOK {
				t.Fatalf("RPC %s receipt status=%d body=%s", request.Operation, receiptResponse.StatusCode, receiptBody)
			}
			var accepted core.Result
			if receiptRPC := decodeRPC(t, receiptBody); len(receiptRPC.Error) != 0 || json.Unmarshal(receiptRPC.Result, &accepted) != nil {
				return nil
			}
			return &accepted
		})
	}
	capResponse, capBody := rpcHTTPSend(t, f, "/v1/rpc", rpcRequestBody(t, "rpc-all-cap", "airc.capabilities", map[string]any{}), false)
	if capResponse.StatusCode != http.StatusOK {
		t.Fatalf("RPC capabilities preflight status=%d body=%s", capResponse.StatusCode, capBody)
	}
	sent := call(core.Request{Version: 1, RequestID: "rpc-all-send", Operation: core.Send, Target: room, Message: "rpc all send"})
	if sent.MessageID == "" {
		t.Fatal("rpc send returned no message id")
	}
	parentID := sent.MessageID
	call(core.Request{Version: 1, RequestID: "rpc-all-reply", Operation: core.Reply, Target: room, ParentID: parentID, Message: "rpc all reply"})
	call(core.Request{Version: 1, RequestID: "rpc-all-reaction", Operation: core.ReactionAdd, Target: room, ParentID: parentID, Reaction: "agree"})
	call(core.Request{Version: 1, RequestID: "rpc-all-profile", Operation: core.ProfileUpdate, Profile: &core.ProfilePatch{About: strptr("rpc all profile")}})
	call(core.Request{Version: 1, RequestID: "rpc-all-presence", Operation: core.PresenceSet, Presence: &core.PresenceInput{State: "away", Note: "rpc all presence", TTLSeconds: 30}})
	call(core.Request{Version: 1, RequestID: "rpc-all-topic", Operation: core.TopicSet, Target: room, Topic: strptr("rpc all topic")})
	slow, history := int64(0), 32
	call(core.Request{Version: 1, RequestID: "rpc-all-room", Operation: core.RoomUpdate, Target: room, Room: &core.RoomPatch{SlowSeconds: &slow, HistoryLimit: &history}})
	call(core.Request{Version: 1, RequestID: "rpc-all-pin-add", Operation: core.PinsAdd, Target: room, ParentID: parentID})
	call(core.Request{Version: 1, RequestID: "rpc-all-pin-remove", Operation: core.PinsRemove, Target: room, ParentID: parentID})
	call(core.Request{Version: 1, RequestID: "rpc-all-activity", Operation: core.ActivitySet, Target: room, Activity: &core.ActivityInput{Kind: "typing", Note: "rpc all", TTLSeconds: 5}})
	call(core.Request{Version: 1, RequestID: "rpc-all-signal-set", Operation: core.ReplySignalSet, Target: room, ParentID: parentID, ReplySignal: &core.ReplySignalInput{Note: "rpc all", TTLSeconds: 5}})
	call(core.Request{Version: 1, RequestID: "rpc-all-signal-cancel", Operation: core.ReplySignalCancel, Target: room, ParentID: parentID})
	poll := call(core.Request{Version: 1, RequestID: "rpc-all-poll-create", Operation: core.PollCreate, Target: room, Poll: &core.PollInput{Question: "rpc all poll", Options: []string{"yes", "no"}, TTLSeconds: 60}})
	if poll.Poll == nil {
		t.Fatalf("poll.create returned no acknowledgement: %+v", poll)
	}
	pollID := poll.Poll.Poll.ID
	call(core.Request{Version: 1, RequestID: "rpc-all-poll-vote", Operation: core.PollVote, Target: room, PollID: pollID, Choice: 1})
	pollReadRequest := core.Request{Version: 1, RequestID: "rpc-all-poll-results", Operation: core.PollResults, Target: room, PollID: pollID}
	pollReadResponse, pollReadBody := rpcHTTPSend(t, f, "/v1/rpc", rpcRequestBody(t, pollReadRequest.RequestID, "airc.request", rpcRequestParams(pollReadRequest)), false)
	if pollReadResponse.StatusCode != http.StatusOK {
		t.Fatalf("poll.results status=%d body=%s", pollReadResponse.StatusCode, pollReadBody)
	}
	pollReadRPC := decodeRPC(t, pollReadBody)
	var pollSnapshot core.PollResult
	if len(pollReadRPC.Error) != 0 || json.Unmarshal(pollReadRPC.Result, &pollSnapshot) != nil || pollSnapshot.ID != pollID {
		t.Fatalf("poll.results snapshot=%s", pollReadRPC.Result)
	}
	call(core.Request{Version: 1, RequestID: "rpc-all-poll-close", Operation: core.PollClose, Target: room, PollID: pollID})
	message := call(core.Request{Version: 1, RequestID: "rpc-all-action", Operation: core.MessageAction, Target: room, Message: "rpc all action"})
	if message.MessageID == "" {
		t.Fatal("message.action returned no message id")
	}
	call(core.Request{Version: 1, RequestID: "rpc-all-correct", Operation: core.MessageCorrect, Target: room, ParentID: message.MessageID, Message: "rpc all corrected"})
	call(core.Request{Version: 1, RequestID: "rpc-all-retract", Operation: core.MessageRetract, Target: room, ParentID: message.MessageID})
}
