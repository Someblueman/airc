//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	v1 "github.com/Someblueman/airc/api/gateway/v1"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func grpcCall(t *testing.T, f grpcGatewayFixture, token string, request core.Request) core.Result {
	t.Helper()
	ctx, cancel := grpcContext(t, token)
	defer cancel()
	response, err := f.client.Call(ctx, &v1.Request{Envelope: grpcEnvelope(t, request)})
	if err != nil {
		t.Fatalf("gRPC %s call: %v", request.Operation, err)
	}
	if len(response.GetResultJson()) == 0 || response.GetError() != nil {
		t.Fatalf("gRPC %s response=%+v", request.Operation, response)
	}
	var result core.Result
	if err := json.Unmarshal(response.GetResultJson(), &result); err != nil {
		t.Fatalf("gRPC %s result=%s: %v", request.Operation, response.GetResultJson(), err)
	}
	return result
}

func grpcReadJSON(t *testing.T, f grpcGatewayFixture, request core.Request) []byte {
	t.Helper()
	ctx, cancel := grpcContext(t, f.token)
	defer cancel()
	response, err := f.client.Call(ctx, &v1.Request{Envelope: grpcEnvelope(t, request)})
	if err != nil || response.GetError() != nil || len(response.GetResultJson()) == 0 {
		t.Fatalf("gRPC read %s response=%+v err=%v", request.Operation, response, err)
	}
	return response.GetResultJson()
}

func grpcAccepted(t *testing.T, f grpcGatewayFixture, request core.Request) core.Result {
	t.Helper()
	result := grpcCall(t, f, f.token, request)
	if !result.Durable {
		t.Fatalf("gRPC %s was not durable: %+v", request.Operation, result)
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		receipt := grpcCall(t, f, f.token, core.Request{Version: 1, RequestID: request.RequestID + "-receipt", Operation: core.ReceiptGet, LookupID: request.RequestID})
		if receipt.State == core.Accepted {
			return receipt
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("gRPC %s did not reach accepted", request.Operation)
	return core.Result{}
}

func TestGRPCCapabilitiesAndAllMutationOperations(t *testing.T) {
	f := newGRPCGatewayFixture(t)
	ctx, cancel := grpcContext(t, f.token)
	defer cancel()
	capabilities, err := f.client.Capabilities(ctx, &v1.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Operations []core.Operation `json:"operations"`
	}
	if err := json.Unmarshal(capabilities.GetResultJson(), &got); err != nil || !reflect.DeepEqual(got.Operations, dispatch.ImplementedOperations()) {
		t.Fatalf("gRPC capabilities=%s err=%v", capabilities.GetResultJson(), err)
	}
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	parent := grpcAccepted(t, f, core.Request{Version: 1, RequestID: "grpc-all-send", Operation: core.Send, Target: room, Message: "grpc all send"})
	if parent.MessageID == "" {
		t.Fatal("gRPC send returned no message id")
	}
	reads := []core.Request{
		{Version: 1, RequestID: "grpc-history", Operation: core.HistoryRead, Target: room, Start: "retained", Limit: 16},
		{Version: 1, RequestID: "grpc-search", Operation: core.Search, Target: room, Query: "grpc all", Limit: 16},
		{Version: 1, RequestID: "grpc-context", Operation: core.ContextRead, Target: room, ParentID: parent.MessageID, Limit: 16},
		{Version: 1, RequestID: "grpc-pins", Operation: core.PinsRead, Target: room},
		{Version: 1, RequestID: "grpc-room", Operation: core.RoomRead, Target: room},
		{Version: 1, RequestID: "grpc-waiting", Operation: core.WaitingRead, Target: room, ParentID: parent.MessageID},
		{Version: 1, RequestID: "grpc-topic", Operation: core.TopicRead, Target: room},
		{Version: 1, RequestID: "grpc-members", Operation: core.MembersRead, Target: room},
		{Version: 1, RequestID: "grpc-channels", Operation: core.ChannelsRead},
		{Version: 1, RequestID: "grpc-directory", Operation: core.Directory, Query: "fixture-author", Limit: 16},
		{Version: 1, RequestID: "grpc-events", Operation: core.EventsWatch, Target: room, Start: "retained", Limit: 16},
	}
	for _, request := range reads {
		data := grpcReadJSON(t, f, request)
		if len(data) < 3 {
			t.Fatalf("gRPC read %s returned empty JSON", request.Operation)
		}
		if request.Operation == core.HistoryRead || request.Operation == core.Search || request.Operation == core.ContextRead {
			var page backend.HistoryPage
			if err := json.Unmarshal(data, &page); err != nil || len(page.Messages) == 0 {
				t.Fatalf("gRPC %s lost message facts: %s err=%v", request.Operation, data, err)
			}
		}
	}
	replyRequest := core.Request{Version: 1, RequestID: "grpc-all-reply", Operation: core.Reply, Target: room, ParentID: parent.MessageID, Message: "grpc all reply"}
	grpcAccepted(t, f, replyRequest)
	replyNative := next(t, f.observer, func(event *irc.MessageEvent) bool { return event.Message == replyRequest.Message })
	if replyNative.ReplyTo != parent.MessageID || replyNative.Target != room.Name {
		t.Fatalf("gRPC native reply=%+v", replyNative)
	}
	requests := []core.Request{
		{Version: 1, RequestID: "grpc-all-reaction", Operation: core.ReactionAdd, Target: room, ParentID: parent.MessageID, Reaction: "agree"},
		{Version: 1, RequestID: "grpc-all-profile", Operation: core.ProfileUpdate, Profile: &core.ProfilePatch{About: strptr("grpc profile")}},
		{Version: 1, RequestID: "grpc-all-presence", Operation: core.PresenceSet, Presence: &core.PresenceInput{State: "away", Note: "grpc presence", TTLSeconds: 30}},
		{Version: 1, RequestID: "grpc-all-topic", Operation: core.TopicSet, Target: room, Topic: strptr("grpc topic")},
		{Version: 1, RequestID: "grpc-all-room", Operation: core.RoomUpdate, Target: room, Room: &core.RoomPatch{SlowSeconds: new(int64), HistoryLimit: grpcIntPtr(32)}},
		{Version: 1, RequestID: "grpc-all-pin-add", Operation: core.PinsAdd, Target: room, ParentID: parent.MessageID},
		{Version: 1, RequestID: "grpc-all-pin-remove", Operation: core.PinsRemove, Target: room, ParentID: parent.MessageID},
		{Version: 1, RequestID: "grpc-all-activity", Operation: core.ActivitySet, Target: room, Activity: &core.ActivityInput{Kind: "typing", Note: "grpc", TTLSeconds: 5}},
		{Version: 1, RequestID: "grpc-all-signal-set", Operation: core.ReplySignalSet, Target: room, ParentID: parent.MessageID, ReplySignal: &core.ReplySignalInput{Note: "grpc", TTLSeconds: 5}},
		{Version: 1, RequestID: "grpc-all-signal-cancel", Operation: core.ReplySignalCancel, Target: room, ParentID: parent.MessageID},
	}
	for _, request := range requests {
		grpcAccepted(t, f, request)
	}
	poll := grpcAccepted(t, f, core.Request{Version: 1, RequestID: "grpc-all-poll-create", Operation: core.PollCreate, Target: room, Poll: &core.PollInput{Question: "grpc poll", Options: []string{"yes", "no"}, TTLSeconds: 60}})
	if poll.Poll == nil {
		t.Fatalf("gRPC poll.create=%+v", poll)
	}
	pollID := poll.Poll.Poll.ID
	if len(grpcReadJSON(t, f, core.Request{Version: 1, RequestID: "grpc-all-poll-results", Operation: core.PollResults, Target: room, PollID: pollID})) < 3 {
		t.Fatal("gRPC poll.results returned empty JSON")
	}
	grpcAccepted(t, f, core.Request{Version: 1, RequestID: "grpc-all-poll-vote", Operation: core.PollVote, Target: room, PollID: pollID, Choice: 1})
	grpcAccepted(t, f, core.Request{Version: 1, RequestID: "grpc-all-poll-close", Operation: core.PollClose, Target: room, PollID: pollID})
	action := grpcAccepted(t, f, core.Request{Version: 1, RequestID: "grpc-all-action", Operation: core.MessageAction, Target: room, Message: "grpc action"})
	if action.MessageID == "" {
		t.Fatal("gRPC message.action returned no message id")
	}
	grpcAccepted(t, f, core.Request{Version: 1, RequestID: "grpc-all-correct", Operation: core.MessageCorrect, Target: room, ParentID: action.MessageID, Message: "grpc corrected"})
	grpcAccepted(t, f, core.Request{Version: 1, RequestID: "grpc-all-retract", Operation: core.MessageRetract, Target: room, ParentID: action.MessageID})
}

func grpcIntPtr(value int) *int { return &value }

func TestGRPCRejectsUnauthenticatedAndWrongPrincipal(t *testing.T) {
	f := newGRPCGatewayFixture(t)
	ctx, cancel := grpcContext(t, "")
	defer cancel()
	_, err := f.client.Capabilities(ctx, &v1.Empty{})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("missing bearer gRPC code=%v err=%v", status.Code(err), err)
	}
	ctx, cancel = grpcContext(t, f.otherToken)
	defer cancel()
	_, err = f.client.Call(ctx, &v1.Request{Envelope: grpcEnvelope(t, core.Request{Version: 1, RequestID: "grpc-denied", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "denied"})})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("wrong principal code=%v err=%v", status.Code(err), err)
	}
}
