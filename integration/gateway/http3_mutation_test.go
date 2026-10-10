//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func http3Accepted(t *testing.T, f http3GatewayFixture, request core.Request) core.Result {
	t.Helper()
	response, data := http3Call(t, f, f.token, "/v1/rpc", rpcRequestBody(t, request.RequestID, "airc.request", rpcRequestParams(request)))
	if response.StatusCode != http.StatusOK || response.ProtoMajor != 3 {
		t.Fatalf("HTTP/3 %s status/protocol=%d/%d body=%s", request.Operation, response.StatusCode, response.ProtoMajor, data)
	}
	admission := decodeRPC(t, data)
	var durable core.Result
	if len(admission.Error) != 0 || json.Unmarshal(admission.Result, &durable) != nil || !durable.Durable {
		t.Fatalf("HTTP/3 %s admission=%s", request.Operation, admission.Result)
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		receiptRequest := core.Request{Version: 1, RequestID: request.RequestID + "-receipt", Operation: core.ReceiptGet, LookupID: request.RequestID}
		response, data = http3Call(t, f, f.token, "/v1/rpc", rpcRequestBody(t, receiptRequest.RequestID, "airc.request", rpcRequestParams(receiptRequest)))
		var receipt core.Result
		if response.StatusCode == http.StatusOK && json.Unmarshal(decodeRPC(t, data).Result, &receipt) == nil && receipt.State == core.Accepted {
			return receipt
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("HTTP/3 %s did not reach accepted", request.Operation)
	return core.Result{}
}

func TestHTTP3AllMutationsReplayAndNativeFacts(t *testing.T) {
	f := newHTTP3GatewayFixture(t)
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	parent := http3Accepted(t, f, core.Request{Version: 1, RequestID: "http3-send", Operation: core.Send, Target: room, Message: "http3 send 🙂"})
	if parent.MessageID == "" {
		t.Fatal("HTTP/3 send returned no message id")
	}
	native := next(t, f.observer, func(event *irc.MessageEvent) bool { return event.ID == parent.MessageID })
	if native.Message != "http3 send 🙂" || native.Target != room.Name {
		t.Fatalf("HTTP/3 native send=%+v", native)
	}
	requests := []core.Request{
		{Version: 1, RequestID: "http3-reply", Operation: core.Reply, Target: room, ParentID: parent.MessageID, Message: "http3 reply"},
		{Version: 1, RequestID: "http3-reaction", Operation: core.ReactionAdd, Target: room, ParentID: parent.MessageID, Reaction: "agree"},
		{Version: 1, RequestID: "http3-profile", Operation: core.ProfileUpdate, Profile: &core.ProfilePatch{About: strptr("http3 profile")}},
		{Version: 1, RequestID: "http3-presence", Operation: core.PresenceSet, Presence: &core.PresenceInput{State: "away", Note: "http3", TTLSeconds: 30}},
		{Version: 1, RequestID: "http3-topic", Operation: core.TopicSet, Target: room, Topic: strptr("http3 topic")},
		{Version: 1, RequestID: "http3-room", Operation: core.RoomUpdate, Target: room, Room: &core.RoomPatch{SlowSeconds: new(int64), HistoryLimit: http3IntPtr(32)}},
		{Version: 1, RequestID: "http3-pin-add", Operation: core.PinsAdd, Target: room, ParentID: parent.MessageID},
		{Version: 1, RequestID: "http3-pin-remove", Operation: core.PinsRemove, Target: room, ParentID: parent.MessageID},
		{Version: 1, RequestID: "http3-activity", Operation: core.ActivitySet, Target: room, Activity: &core.ActivityInput{Kind: "typing", Note: "http3", TTLSeconds: 5}},
		{Version: 1, RequestID: "http3-signal-set", Operation: core.ReplySignalSet, Target: room, ParentID: parent.MessageID, ReplySignal: &core.ReplySignalInput{Note: "http3", TTLSeconds: 5}},
		{Version: 1, RequestID: "http3-signal-cancel", Operation: core.ReplySignalCancel, Target: room, ParentID: parent.MessageID},
	}
	for _, request := range requests {
		http3Accepted(t, f, request)
	}
	poll := http3Accepted(t, f, core.Request{Version: 1, RequestID: "http3-poll", Operation: core.PollCreate, Target: room, Poll: &core.PollInput{Question: "http3 poll", Options: []string{"yes", "no"}, TTLSeconds: 60}})
	if poll.Poll == nil {
		t.Fatalf("HTTP/3 poll=%+v", poll)
	}
	pollID := poll.Poll.Poll.ID
	http3Accepted(t, f, core.Request{Version: 1, RequestID: "http3-vote", Operation: core.PollVote, Target: room, PollID: pollID, Choice: 1})
	resultsResponse, resultsData := http3Call(t, f, f.token, "/v1/rpc", rpcRequestBody(t, "http3-poll-results", "airc.request", rpcRequestParams(core.Request{Version: 1, RequestID: "http3-poll-results", Operation: core.PollResults, Target: room, PollID: pollID})))
	if resultsResponse.StatusCode != http.StatusOK || resultsResponse.ProtoMajor != 3 {
		t.Fatalf("HTTP/3 poll.results status/protocol=%d/%d body=%s", resultsResponse.StatusCode, resultsResponse.ProtoMajor, resultsData)
	}
	var results core.PollResult
	if err := json.Unmarshal(decodeRPC(t, resultsData).Result, &results); err != nil || results.ID != pollID || len(results.Votes) != 2 || results.Votes[0]+results.Votes[1] != 1 {
		t.Fatalf("HTTP/3 poll.results=%+v body=%s err=%v", results, resultsData, err)
	}
	http3Accepted(t, f, core.Request{Version: 1, RequestID: "http3-close", Operation: core.PollClose, Target: room, PollID: pollID})
	action := http3Accepted(t, f, core.Request{Version: 1, RequestID: "http3-action", Operation: core.MessageAction, Target: room, Message: "http3 action"})
	http3Accepted(t, f, core.Request{Version: 1, RequestID: "http3-correct", Operation: core.MessageCorrect, Target: room, ParentID: action.MessageID, Message: "http3 corrected"})
	http3Accepted(t, f, core.Request{Version: 1, RequestID: "http3-retract", Operation: core.MessageRetract, Target: room, ParentID: action.MessageID})
	first := http3Accepted(t, f, core.Request{Version: 1, RequestID: "http3-replay", Operation: core.Send, Target: room, Message: "http3 replay"})
	second := http3Accepted(t, f, core.Request{Version: 1, RequestID: "http3-replay", Operation: core.Send, Target: room, Message: "http3 replay"})
	if first.MessageID != second.MessageID {
		t.Fatalf("HTTP/3 replay changed durable ID: first=%s second=%s", first.MessageID, second.MessageID)
	}
}

func http3IntPtr(value int) *int { return &value }
