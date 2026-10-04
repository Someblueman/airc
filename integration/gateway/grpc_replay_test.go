//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	v1 "github.com/Someblueman/airc/api/gateway/v1"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestGRPCReplayAndWholeNativeHistorySearchContext(t *testing.T) {
	f := newGRPCGatewayFixture(t)
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	send := core.Request{Version: 1, RequestID: "grpc-replay-send", Operation: core.Send, Target: room, Message: "gRPC héllo 🌍\nsecond line"}
	first := grpcAccepted(t, f, send)
	native := next(t, f.observer, func(event *irc.MessageEvent) bool { return event.Message == send.Message })
	second := grpcAccepted(t, f, send)
	if first.MessageID != native.ID || second.MessageID != first.MessageID || second.SendAttempts != 1 {
		t.Fatalf("durable replay first=%+v second=%+v native=%+v", first, second, native)
	}
	reply := core.Request{Version: 1, RequestID: "grpc-replay-reply", Operation: core.Reply, Target: room, ParentID: first.MessageID, Message: "gRPC reply"}
	accepted := grpcAccepted(t, f, reply)
	grpcAccepted(t, f, reply)
	replyNative := next(t, f.observer, func(event *irc.MessageEvent) bool { return event.Message == reply.Message })
	if replyNative.ID != accepted.MessageID || replyNative.ReplyTo != native.ID || replyNative.ThreadID != native.ID {
		t.Fatalf("native reply=%+v", replyNative)
	}
	probe := dial(t, f.config.Bindings[0].Address, "grpc-native-check", "", false, true)
	defer probe.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	entries, err := irc.RequestCheck(ctx, probe, irc.CheckRequest{Targets: []irc.CheckTarget{{Target: room.Name, After: "*", Limit: 16}}, IncludeOwn: true, MaxMessages: 16}, nil)
	var messages []*irc.MessageMetadata
	for _, entry := range entries {
		if entry.Message != nil {
			messages = append(messages, entry.Message)
		}
	}
	if err != nil || len(messages) != 2 {
		t.Fatalf("native exact count entries=%+v err=%v", entries, err)
	}
	for i, operation := range []core.Operation{core.HistoryRead, core.Search, core.ContextRead, core.Watch} {
		request := core.Request{Version: 1, RequestID: fmt.Sprintf("grpc-whole-%d", i), Operation: operation, Target: room, Limit: 16}
		if operation == core.Search {
			request.Query = "gRPC"
		} else if operation == core.ContextRead {
			request.ParentID = native.ID
		} else {
			request.Start = "retained"
		}
		var page backend.HistoryPage
		data := grpcReadJSON(t, f, request)
		if json.Unmarshal(data, &page) != nil || !reflect.DeepEqual(page.Messages, messages) {
			t.Fatalf("%s differs from whole native CHECK: result=%s native=%+v", operation, data, messages)
		}
	}
	changed := send
	changed.Message = "conflicting durable intent"
	authorized, finish := grpcContext(t, f.token)
	defer finish()
	result, err := f.client.Call(authorized, &v1.Request{Envelope: grpcEnvelope(t, changed)})
	if err != nil || result.GetError().GetCode() != string(core.IDConflict) {
		t.Fatalf("durable conflict result=%+v err=%v", result, err)
	}
}
