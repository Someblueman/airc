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

func assertNativeReadFacts(t *testing.T, f nativeOperationFixture, request core.Request, result *dispatch.DetachedResult) {
	t.Helper()
	if result == nil || result.Operation != request.Operation {
		t.Fatalf("carrier %s missing result=%+v", request.Operation, result)
	}
	socket := f.config.Upstream.Address
	room := "#gateway-fixture"
	switch request.Operation {
	case core.ContextRead, core.HistoryRead, core.Search, core.Watch, core.EventsWatch:
		var messages []*irc.MessageMetadata
		if request.Operation == core.ContextRead {
			if result.Context == nil {
				t.Fatal("carrier context missing")
			}
			for i := range result.Context.Messages {
				messages = append(messages, &result.Context.Messages[i])
			}
		} else {
			if result.Page == nil {
				t.Fatalf("carrier %s page missing", request.Operation)
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
			t.Fatalf("carrier %s whole native records mismatch: got=%+v want=%+v", request.Operation, messages, expected)
		}
	case core.PinsRead:
		if result.Pins == nil || result.Pins.Target != *request.Target || !reflect.DeepEqual(result.Pins.Pins, queryPinMutationNative(t, socket, room)) {
			t.Fatalf("carrier native pins mismatch=%+v", result.Pins)
		}
	case core.RoomRead:
		if result.Room == nil || *result.Room != queryNativeRoom(t, socket, room) {
			t.Fatalf("carrier native room mismatch=%+v", result.Room)
		}
	case core.TopicRead:
		if result.Topic == nil || *result.Topic != queryNativeTopic(t, socket, room) {
			t.Fatalf("carrier native topic mismatch=%+v", result.Topic)
		}
	case core.WaitingRead:
		expected := queryNativeWaitingSignal(t, socket, request.ParentID)
		if result.Waiting == nil || result.Waiting.Target != *request.Target || result.Waiting.ParentID != request.ParentID || result.Waiting.Signals == nil || len(result.Waiting.Signals) != len(expected) || len(expected) > 0 && !reflect.DeepEqual(result.Waiting.Signals, expected) {
			t.Fatalf("carrier native waiting mismatch=%+v", result.Waiting)
		}
	case core.MembersRead:
		if result.Members == nil || result.Members.Target != *request.Target || !reflect.DeepEqual(result.Members.Members, nativeMembers(t, socket, room)) {
			t.Fatalf("carrier native members mismatch=%+v", result.Members)
		}
	case core.ChannelsRead:
		if result.Channels == nil || !reflect.DeepEqual(result.Channels.Channels, nativeChannels(t, socket)) {
			t.Fatalf("carrier native channels mismatch=%+v", result.Channels)
		}
	default:
		t.Fatalf("carrier read check missing operation %s", request.Operation)
	}
}
