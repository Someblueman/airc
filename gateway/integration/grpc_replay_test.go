//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestGRPCReplayAndWholeNativeHistorySearchContext(t *testing.T) {
	f := newGRPCGatewayFixture(t)
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	send := core.Request{Version: 1, RequestID: "grpc-replay-send", Operation: core.Send, Target: room, Message: "gRPC héllo 🌍\nsecond line"}
	first := grpcAccepted(t, f, send)
	native := next(t, f.observer, func(event *irc.MessageEvent) bool { return event.Message == send.Message })
	second := grpcAccepted(t, f, send)
	if first.MessageID != native.ID || second.MessageID != first.MessageID || second.SendAttempts != 1 || native.From != "fixture-author" || native.Target != room.Name {
		t.Fatalf("durable replay first=%+v second=%+v native=%+v", first, second, native)
	}
	reply := core.Request{Version: 1, RequestID: "grpc-replay-reply", Operation: core.Reply, Target: room, ParentID: first.MessageID, Message: "gRPC reply"}
	accepted := grpcAccepted(t, f, reply)
	grpcAccepted(t, f, reply)
	replyNative := next(t, f.observer, func(event *irc.MessageEvent) bool { return event.Message == reply.Message })
	if replyNative.ID != accepted.MessageID || replyNative.ReplyTo != native.ID || replyNative.ThreadID != native.ID || replyNative.From != "fixture-author" || replyNative.Target != room.Name {
		t.Fatalf("native reply=%+v", replyNative)
	}
	messages := nativeWebTransportMessages(t, f.config.Upstream.Address, room.Name)
	if len(messages) != 2 {
		t.Fatalf("native exact count %+v", messages)
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
}
