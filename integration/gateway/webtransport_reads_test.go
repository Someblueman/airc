//go:build darwin || linux

package gateway_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
)

func webTransportBidiCall(t *testing.T, f webTransportFixture, request core.Request, encrypted bool) dispatch.DetachedResult {
	t.Helper()
	stream, err := f.session.OpenStreamSync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	frame := adapterFrameRequest(t, f, request, encrypted)
	if _, err := stream.Write(webTransportFrame(t, frame)); err != nil {
		t.Fatal(err)
	}
	response := readWebTransportFrame(t, stream)
	if response.Event != "result" || response.Result == nil || response.Result.Operation != request.Operation || response.CorrelationID != frame.CorrelationID {
		if response.Error != nil {
			t.Fatalf("WebTransport %s response error=%s upstream=%d", request.Operation, response.Error.Code, response.Error.UpstreamCode)
		}
		t.Fatalf("WebTransport %s response=%+v", request.Operation, response)
	}
	return *response.Result
}

func adapterFrameRequest(t *testing.T, f webTransportFixture, request core.Request, encrypted bool) frameRequest {
	t.Helper()
	return frameRequest{Version: 1, CorrelationID: "read-" + request.RequestID, Profile: "plain", Request: f.credentials.token(t, request, encrypted)}
}

func nativeWebTransportMessages(t *testing.T, socket, room string) []*irc.MessageMetadata {
	t.Helper()
	probe := dial(t, socket, "webtransport-check", "", false, true)
	defer probe.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	entries, err := irc.RequestCheck(ctx, probe, irc.CheckRequest{Targets: []irc.CheckTarget{{Target: room, After: "*", Limit: 16}}, IncludeOwn: true, MaxMessages: 16}, nil)
	if err != nil {
		t.Fatal(err)
	}
	messages := make([]*irc.MessageMetadata, 0, len(entries))
	for _, entry := range entries {
		if entry.Message != nil {
			messages = append(messages, entry.Message)
		}
	}
	return messages
}

func TestWebTransportReadFactsAndCapabilities(t *testing.T) {
	f := newWebTransportFixture(t)
	parentID := seedPinMutationMessage(t, f.config.Upstream.Address, "#gateway-fixture", "webtransport-parent")
	native := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == parentID })
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	nativeMessages := nativeWebTransportMessages(t, f.config.Upstream.Address, room.Name)
	capabilities := webTransportBidiCall(t, f, core.Request{Version: 1, RequestID: "capabilities", Operation: core.CapabilitiesGet}, false)
	if capabilities.Capabilities == nil || !reflect.DeepEqual(capabilities.Capabilities.Operations, dispatch.ImplementedOperations()) {
		t.Fatalf("WebTransport capabilities=%+v", capabilities.Capabilities)
	}
	access := capabilities.Capabilities
	if !reflect.DeepEqual(access.WatchModes, []string{"long_poll", "webtransport_stream"}) || access.MaxRequestFrameBytes != adapter.MaxRequestFrameBytes || access.MaxResponseFrameBytes != adapter.MaxResponseFrameBytes || len(access.Targets) != 0 || access.MaxHistoryBytes == 0 || access.MaxContextBytes != core.MaxContextBytes || access.MaxRoomBytes != core.MaxRoomBytes || access.MaxWaitingSignals != core.MaxWaitingSignals || access.MaxWaitingBytes != core.MaxWaitingBytes || access.MaxPins != core.MaxPins || access.MaxMembers != core.MaxMembers || access.MaxChannels != core.MaxChannels {
		t.Fatalf("WebTransport capability limits/targets=%+v", access)
	}
	reads := []core.Request{
		{Version: 1, RequestID: "history", Operation: core.HistoryRead, Target: room, Start: "retained", Limit: 8},
		{Version: 1, RequestID: "search", Operation: core.Search, Target: room, Query: "pin mutation", Limit: 8},
		{Version: 1, RequestID: "context", Operation: core.ContextRead, Target: room, ParentID: parentID, Limit: 8},
		{Version: 1, RequestID: "events", Operation: core.EventsWatch, Target: room, Start: "retained", Limit: 8},
	}
	for _, request := range reads {
		result := webTransportBidiCall(t, f, request, true)
		var messages []*irc.MessageMetadata
		if request.Operation == core.ContextRead {
			if result.Context != nil {
				for i := range result.Context.Messages {
					messages = append(messages, &result.Context.Messages[i])
				}
			}
		} else if result.Page != nil {
			messages = result.Page.Messages
		}
		if len(nativeMessages) != 1 || !reflect.DeepEqual(messages, nativeMessages) {
			t.Fatalf("WebTransport %s whole native CHECK facts=%+v want=%+v", request.Operation, messages, nativeMessages)
		}
	}
	for _, request := range []core.Request{
		{Version: 1, RequestID: "pins", Operation: core.PinsRead, Target: room},
		{Version: 1, RequestID: "room", Operation: core.RoomRead, Target: room},
		{Version: 1, RequestID: "waiting", Operation: core.WaitingRead, Target: room, ParentID: parentID},
		{Version: 1, RequestID: "topic", Operation: core.TopicRead, Target: room},
		{Version: 1, RequestID: "members", Operation: core.MembersRead, Target: room},
		{Version: 1, RequestID: "channels", Operation: core.ChannelsRead},
		{Version: 1, RequestID: "directory", Operation: core.Directory, Query: "fixture-author", Limit: 8},
	} {
		result := webTransportBidiCall(t, f, request, true)
		switch request.Operation {
		case core.PinsRead:
			if result.Pins == nil || result.Pins.Target != *room || !reflect.DeepEqual(result.Pins.Pins, queryPinMutationNative(t, f.config.Upstream.Address, room.Name)) {
				t.Fatalf("WebTransport pins mismatch=%+v", result.Pins)
			}
		case core.RoomRead:
			if result.Room == nil || result.Room.Target != *room || *result.Room != queryNativeRoom(t, f.config.Upstream.Address, room.Name) {
				t.Fatalf("WebTransport room mismatch=%+v", result.Room)
			}
		case core.TopicRead:
			if result.Topic == nil || result.Topic.Target != *room || *result.Topic != queryNativeTopic(t, f.config.Upstream.Address, room.Name) {
				t.Fatalf("WebTransport topic mismatch=%+v", result.Topic)
			}
		case core.MembersRead:
			if result.Members == nil || result.Members.Target != *room || !reflect.DeepEqual(result.Members.Members, nativeMembers(t, f.config.Upstream.Address, room.Name)) {
				t.Fatalf("WebTransport members mismatch=%+v", result.Members)
			}
		case core.ChannelsRead:
			if result.Channels == nil || !reflect.DeepEqual(result.Channels.Channels, nativeChannels(t, f.config.Upstream.Address)) {
				t.Fatalf("WebTransport channels mismatch=%+v", result.Channels)
			}
		case core.Directory:
			if result.Directory == nil || len(result.Directory.Agents) != 1 || result.Directory.Agents[0].Nick != "fixture-author" {
				t.Fatalf("WebTransport directory mismatch=%+v", result.Directory)
			}
		case core.WaitingRead:
			if result.Waiting == nil || result.Waiting.Target != *room || result.Waiting.ParentID != parentID || result.Waiting.Signals == nil {
				t.Fatalf("WebTransport waiting mismatch=%+v", result.Waiting)
			}
		}
	}
	historyAgain := webTransportBidiCall(t, f, core.Request{Version: 1, RequestID: "history-again", Operation: core.HistoryRead, Target: room, Start: "retained", Limit: 8}, true)
	if historyAgain.Page == nil || len(historyAgain.Page.Messages) == 0 || historyAgain.Page.Messages[0].ID != native.ID {
		t.Fatalf("WebTransport history re-read failed: %+v", historyAgain.Page)
	}
}
