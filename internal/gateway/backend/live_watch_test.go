package backend

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestLivePageKeepsMessageCheckpointAndCombinedBudget(t *testing.T) {
	id := strings.Repeat("a", 32)
	r := core.Request{Operation: core.EventsWatch, Target: &core.Target{Kind: "room", Name: "#room"}, Limit: 2}
	topic := "changed"
	buffer := &watchBuffer{request: r, events: []*LiveEvent{{Kind: "topic", Target: "#room", Actor: "writer", Topic: &topic}}}
	page := HistoryPage{Messages: []*irc.MessageMetadata{}, Cursor: id, Status: "ok"}
	got, err := mergeLiveEvents(t.Context(), nil, page, buffer)
	if err != nil || got.Cursor != id || len(got.Messages) != 0 || len(got.Events) != 1 {
		t.Fatal("nonmessage event changed checkpoint", got, err)
	}
	message := &irc.MessageMetadata{ID: strings.Repeat("b", 32), Target: "#room", From: "writer", Message: "hello", Timestamp: time.Now()}
	buffer.messages = []*irc.MessageMetadata{message}
	got, err = mergeLiveEvents(t.Context(), nil, page, buffer)
	if err != nil || got.Cursor != message.ID || len(got.Messages) != 1 || len(got.Events) != 1 {
		t.Fatal("mixed records lost message position", got, err)
	}
	buffer.request.Limit = 1
	if got, err := mergeLiveEvents(t.Context(), nil, page, buffer); !errors.Is(err, store.ErrCapacity) || len(got.Events) != 0 || len(got.Messages) != 0 {
		t.Fatal("combined record limit returned partial data", got, err)
	}
}

func TestLiveConversationSignalsRequireParentScope(t *testing.T) {
	parent := strings.Repeat("a", 32)
	r := core.Request{Operation: core.EventsWatch, Target: &core.Target{Kind: "room", Name: "#room"}, Limit: 2, ThreadMode: "replies", ParentID: parent}
	buffer := &watchBuffer{request: r, events: []*LiveEvent{{Kind: "signal", Signal: &irc.ChatEntry{ID: strings.Repeat("b", 32)}}}}
	page := HistoryPage{Messages: []*irc.MessageMetadata{}, Status: "ok"}
	if _, err := mergeLiveEvents(t.Context(), nil, page, buffer); !errors.Is(err, ErrReadUnavailable) {
		t.Fatal("foreign reply signal accepted", err)
	}
	buffer.events[0].Signal.ID = parent
	if _, err := mergeLiveEvents(t.Context(), nil, page, buffer); err != nil {
		t.Fatal("authorized reply signal rejected", err)
	}
	buffer.request.ThreadMode = "thread"
	buffer.known = map[string]bool{parent: true}
	if _, err := mergeLiveEvents(t.Context(), nil, page, buffer); err != nil {
		t.Fatal("validated thread parent rejected", err)
	}
	buffer.events[0] = &LiveEvent{Kind: "quit", Target: "#room", Actor: "writer"}
	if _, err := mergeLiveEvents(t.Context(), nil, page, buffer); !errors.Is(err, ErrReadUnavailable) {
		t.Fatal("room membership event leaked through thread selector", err)
	}
}

func TestLegacyWatchRemainsMessageOnly(t *testing.T) {
	buffer := &watchBuffer{request: core.Request{Operation: core.Watch}}
	if err := buffer.collect(&irc.JoinEvent{Agent: "writer", Channel: "#room"}); err != nil || len(buffer.events) != 0 {
		t.Fatal("legacy watch collected membership", err)
	}
	data, err := json.Marshal(HistoryPage{Messages: []*irc.MessageMetadata{}, Status: "ok"})
	if err != nil || strings.Contains(string(data), `"events"`) {
		t.Fatal("legacy page shape changed", string(data), err)
	}
}
