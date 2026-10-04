//go:build darwin || linux

package gateway_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
)

func assertRawQUICReadFacts(t *testing.T, f rawQUICFixture, request core.Request, result *dispatch.DetachedResult) {
	t.Helper()
	if result == nil || result.Operation != request.Operation {
		t.Fatalf("raw QUIC %s missing result=%+v", request.Operation, result)
	}
	socket := f.config.Bindings[0].Address
	room := "#gateway-fixture"
	switch request.Operation {
	case core.ContextRead, core.HistoryRead, core.Search, core.Watch, core.EventsWatch:
		var messages []*irc.MessageMetadata
		if request.Operation == core.ContextRead {
			if result.Context == nil {
				t.Fatal("raw QUIC context missing")
			}
			for i := range result.Context.Messages {
				messages = append(messages, &result.Context.Messages[i])
			}
		} else {
			if result.Page == nil {
				t.Fatalf("raw QUIC %s page missing", request.Operation)
			}
			messages = result.Page.Messages
		}
		expected := nativeWebTransportMessages(t, socket, room)
		if request.Operation == core.Search {
			filtered := make([]*irc.MessageMetadata, 0, len(expected))
			for _, message := range expected {
				if strings.Contains(message.Message, request.Query) {
					filtered = append(filtered, message)
				}
			}
			expected = filtered
		}
		if len(expected) == 0 || !reflect.DeepEqual(messages, expected) {
			t.Fatalf("raw QUIC %s whole native records mismatch: got=%+v want=%+v", request.Operation, messages, expected)
		}
	case core.PinsRead:
		if result.Pins == nil || result.Pins.Target != *request.Target || !reflect.DeepEqual(result.Pins.Pins, queryPinMutationNative(t, socket, room)) {
			t.Fatalf("raw QUIC native pins mismatch=%+v", result.Pins)
		}
	case core.RoomRead:
		if result.Room == nil || *result.Room != queryNativeRoom(t, socket, room) {
			t.Fatalf("raw QUIC native room mismatch=%+v", result.Room)
		}
	case core.TopicRead:
		if result.Topic == nil || *result.Topic != queryNativeTopic(t, socket, room) {
			t.Fatalf("raw QUIC native topic mismatch=%+v", result.Topic)
		}
	case core.WaitingRead:
		expected := queryNativeWaitingSignal(t, socket, request.ParentID)
		if result.Waiting == nil || result.Waiting.Target != *request.Target || result.Waiting.ParentID != request.ParentID || result.Waiting.Signals == nil || len(result.Waiting.Signals) != len(expected) || len(expected) > 0 && !reflect.DeepEqual(result.Waiting.Signals, expected) {
			t.Fatalf("raw QUIC native waiting mismatch=%+v", result.Waiting)
		}
	case core.MembersRead:
		if result.Members == nil || result.Members.Target != *request.Target || !reflect.DeepEqual(result.Members.Members, nativeMembers(t, socket, room)) {
			t.Fatalf("raw QUIC native members mismatch=%+v", result.Members)
		}
	case core.ChannelsRead:
		if result.Channels == nil || !reflect.DeepEqual(result.Channels.Channels, nativeChannels(t, socket)) {
			t.Fatalf("raw QUIC native channels mismatch=%+v", result.Channels)
		}
	default:
		t.Fatalf("raw QUIC read check missing operation %s", request.Operation)
	}
}
