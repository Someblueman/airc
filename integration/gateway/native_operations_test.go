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

func assertNativeCapabilities(t *testing.T, response adapter.FrameResponse, maxResponse int) {
	t.Helper()
	if response.Result == nil || response.Result.Capabilities == nil {
		t.Fatalf("carrier capabilities=%+v", response)
	}
	caps := response.Result.Capabilities
	if !reflect.DeepEqual(caps.Operations, dispatch.ImplementedOperations()) || caps.MaxRequestFrameBytes != adapter.MaxRequestFrameBytes || caps.MaxResponseFrameBytes != maxResponse || len(caps.WatchModes) < 2 {
		t.Fatalf("carrier incomplete capabilities=%+v", caps)
	}
}

func exerciseCarrierSendAndReads(t *testing.T, call nativeOperationCall, accepted nativeAcceptedCall, f nativeOperationFixture, label string) {
	t.Helper()
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	caps := call(core.Request{Version: 1, RequestID: label + "-capabilities", Operation: core.CapabilitiesGet}, false)
	assertNativeCapabilities(t, caps, adapter.MaxResponseFrameBytes)
	expectedMode := map[string]string{"quic-bidi": "quic_stream", "quic-uni": "quic_uni_stream", "quic-datagram": "quic_datagrams", "dtls-datagram": "dtls_datagrams", "udp-datagram": "udp_mailbox_datagrams", "webrtc-reliable_ordered": "webrtc_reliable_ordered", "webrtc-reliable_unordered": "webrtc_reliable_unordered", "webrtc-unreliable_ordered": "webrtc_unreliable_ordered", "webrtc-unreliable_unordered": "webrtc_unreliable_unordered", "coaps_tcp-signed": "coaps_tcp_observe_fragments", "coaps_tcp-encrypted": "coaps_tcp_observe_fragments", "coaps-signed": "coaps_observe_fragments", "coaps-encrypted": "coaps_observe_fragments", "coaps-non-signed": "coaps_observe_fragments", "coaps-non-encrypted": "coaps_observe_fragments"}[label]
	if label == "unixgram" {
		expectedMode = "unixgram_mailbox_datagrams"
	}
	if !reflect.DeepEqual(caps.Result.Capabilities.WatchModes, []string{"long_poll", expectedMode}) {
		t.Fatalf("%s capability scope/modes=%+v", label, caps.Result.Capabilities)
	}

	send := core.Request{Version: 1, RequestID: label + "-send", Operation: core.Send, Target: room, Message: label + " native whole body 🙂"}
	sent := accepted(send, true)
	if sent.MessageID == "" || sent.RequestID != send.RequestID || sent.State != core.Accepted {
		t.Fatalf("%s send result=%+v", label, sent)
	}
	records := nativeWebTransportMessages(t, f.config.Upstream.Address, room.Name)
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
	// Reply linkage and reaction semantics belong to shared backend acceptance.
	// This carrier keeps its actual send, scoped receipt and complete read checks.

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
	if directory.Result == nil || directory.Result.Directory == nil || directory.Result.Directory.Version != 1 || !reflect.DeepEqual(directory.Result.Directory.Agents, []irc.AgentCard{nativeCard(t, f.config.Upstream.Address, "fixture-author")}) {
		t.Fatalf("%s directory=%+v", label, directory)
	}
	agents := call(core.Request{Version: 1, RequestID: label + "-agents", Operation: core.AgentsRead, Limit: 256}, true)
	if agents.Result == nil || agents.Result.Agents == nil || agents.Result.Agents.Version != 1 || !reflect.DeepEqual(agents.Result.Agents.Agents, nativeAgents(t, f.config.Upstream.Address)) {
		t.Fatalf("%s live agents=%+v", label, agents)
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

	exerciseCarrierSeededReads(t, call, f, label, sent)
}
