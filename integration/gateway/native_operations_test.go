//go:build darwin || linux

package gateway_test

import (
	"reflect"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
)

type nativeOperationFixture struct {
	config   config.Config
	observer *irc.Client
}

type nativeOperationCall func(core.Request, bool) adapter.FrameResponse
type nativeAcceptedCall func(core.Request, bool) core.Result

func assertNativeCapabilities(t *testing.T, response adapter.FrameResponse) {
	t.Helper()
	if response.Result == nil || response.Result.Capabilities == nil {
		t.Fatalf("carrier capabilities=%+v", response)
	}
	caps := response.Result.Capabilities
	if !reflect.DeepEqual(caps.Operations, dispatch.ImplementedOperations()) || caps.MaxRequestFrameBytes != adapter.MaxRequestFrameBytes || caps.MaxResponseFrameBytes != adapter.MaxResponseFrameBytes || len(caps.RoomTargets) != 1 || caps.RoomTargets[0].Name != "#gateway-fixture" || len(caps.WatchModes) < 2 {
		t.Fatalf("carrier incomplete capabilities=%+v", caps)
	}
}

func exerciseNativeOperations(t *testing.T, call nativeOperationCall, accepted nativeAcceptedCall, f nativeOperationFixture, label string) {
	t.Helper()
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	caps := call(core.Request{Version: 1, RequestID: label + "-capabilities", Operation: core.CapabilitiesGet}, false)
	assertNativeCapabilities(t, caps)
	expectedMode := map[string]string{"quic-bidi": "quic_stream", "quic-uni": "quic_uni_stream", "quic-datagram": "quic_datagrams", "dtls-datagram": "dtls_datagrams"}[label]
	if !reflect.DeepEqual(caps.Result.Capabilities.WatchModes, []string{"long_poll", expectedMode}) || !reflect.DeepEqual(caps.Result.Capabilities.Targets, f.config.Principals[0].Targets) {
		t.Fatalf("%s capability scope/modes=%+v", label, caps.Result.Capabilities)
	}

	send := core.Request{Version: 1, RequestID: label + "-send", Operation: core.Send, Target: room, Message: label + " native whole body 🙂"}
	sent := accepted(send, true)
	if sent.MessageID == "" || sent.RequestID != send.RequestID || sent.State != core.Accepted {
		t.Fatalf("%s send result=%+v", label, sent)
	}
	records := nativeWebTransportMessages(t, f.config.Bindings[0].Address, room.Name)
	found := false
	for _, record := range records {
		if record.ID == sent.MessageID && record.Message == send.Message && record.Target == room.Name {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("%s native send record missing id=%s records=%+v", label, sent.MessageID, records)
	}

	reply := core.Request{Version: 1, RequestID: label + "-reply", Operation: core.Reply, Target: room, ParentID: sent.MessageID, Message: label + " reply"}
	replyResult := accepted(reply, true)
	if replyResult.MessageID == "" || replyResult.State != core.Accepted {
		t.Fatalf("%s reply result=%+v", label, replyResult)
	}
	records = nativeWebTransportMessages(t, f.config.Bindings[0].Address, room.Name)
	var replyFound bool
	for _, record := range records {
		if record.ID == replyResult.MessageID && record.Message == reply.Message && record.ReplyTo == sent.MessageID && record.ThreadID == sent.MessageID {
			replyFound = true
		}
	}
	if !replyFound {
		t.Fatalf("%s native reply facts missing records=%+v", label, records)
	}
	if result := accepted(core.Request{Version: 1, RequestID: label + "-reaction", Operation: core.ReactionAdd, Target: room, ParentID: sent.MessageID, Reaction: "agree"}, true); result.State != core.Accepted {
		t.Fatalf("%s reaction result=%+v", label, result)
	}
	nativeReaction := next(t, f.observer, func(event *irc.MessageEvent) bool {
		return event.Reaction == "agree" && event.ReplyTo == sent.MessageID && event.ThreadID == sent.MessageID
	})
	if nativeReaction.Message != "agree" || nativeReaction.Target != room.Name || nativeReaction.From == "" {
		t.Fatalf("%s native reaction=%+v", label, nativeReaction)
	}

	for _, request := range []core.Request{
		{Version: 1, RequestID: label + "-context", Operation: core.ContextRead, Target: room, ParentID: sent.MessageID, Limit: 8},
		{Version: 1, RequestID: label + "-pins-read", Operation: core.PinsRead, Target: room},
		{Version: 1, RequestID: label + "-room-read", Operation: core.RoomRead, Target: room},
		{Version: 1, RequestID: label + "-waiting", Operation: core.WaitingRead, Target: room, ParentID: sent.MessageID},
		{Version: 1, RequestID: label + "-history", Operation: core.HistoryRead, Target: room, Start: "retained", Limit: 16},
		{Version: 1, RequestID: label + "-search", Operation: core.Search, Target: room, Query: label, Limit: 16},
		{Version: 1, RequestID: label + "-watch", Operation: core.Watch, Target: room, Start: "retained", Limit: 16},
		{Version: 1, RequestID: label + "-events", Operation: core.EventsWatch, Target: room, Start: "retained", Limit: 16},
	} {
		response := call(request, true)
		assertNativeReadFacts(t, f, request, response.Result)
	}

	directory := call(core.Request{Version: 1, RequestID: label + "-directory", Operation: core.Directory, Query: "fixture-author", Limit: 8}, true)
	if directory.Result == nil || directory.Result.Directory == nil || directory.Result.Directory.Version != 1 || !reflect.DeepEqual(directory.Result.Directory.Agents, []irc.AgentCard{nativeCard(t, f.config.Bindings[0].Address, "fixture-author")}) {
		t.Fatalf("%s directory=%+v", label, directory)
	}
	for _, request := range []core.Request{
		{Version: 1, RequestID: label + "-topic-read", Operation: core.TopicRead, Target: room},
		{Version: 1, RequestID: label + "-members", Operation: core.MembersRead, Target: room},
		{Version: 1, RequestID: label + "-channels", Operation: core.ChannelsRead},
	} {
		response := call(request, true)
		assertNativeReadFacts(t, f, request, response.Result)
	}
	receipt := call(core.Request{Version: 1, RequestID: label + "-receipt", Operation: core.ReceiptGet, LookupID: send.RequestID}, true)
	if receipt.Result == nil || receipt.Result.Result == nil || receipt.Result.Result.RequestID != send.RequestID || receipt.Result.Result.MessageID != sent.MessageID {
		t.Fatalf("%s receipt=%+v", label, receipt)
	}

	exerciseNativeMutations(t, call, accepted, f, label, sent)
}
