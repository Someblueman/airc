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

func TestLiveSignalWireBudgetCountsWhitespace(t *testing.T) {
	body := strings.Repeat(" ", 4096) + `{"action":"typing","target":"#room","from":"writer","expires_at":"2026-10-04T12:00:00Z"}`
	buffer := &watchBuffer{request: core.Request{Operation: core.EventsWatch, Target: &core.Target{Kind: "room", Name: "#room"}, Limit: 1000}, nick: "reader"}
	for i := 0; i < 20; i++ {
		err := buffer.collect(&irc.RawEvent{Type: "raw", Command: "780", Prefix: "server", Params: []string{"reader"}, Trailing: body})
		if errors.Is(err, store.ErrCapacity) {
			if buffer.bytes >= MaxHistoryBytes {
				t.Fatal("test exhausted encoded budget instead of wire budget")
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("unbounded whitespace on native signal wire")
}

func TestLiveSetupEventsHaveLeaseRecordBound(t *testing.T) {
	buffer := &watchBuffer{request: core.Request{Operation: core.EventsWatch, Target: &core.Target{Kind: "room", Name: "#room"}, Limit: 1000}, nick: "reader"}
	for range 128 {
		if err := buffer.collect(&irc.JoinEvent{Type: "join", Agent: "writer", Channel: "#room", Timestamp: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if err := buffer.collect(&irc.JoinEvent{Type: "join", Agent: "writer", Channel: "#room", Timestamp: time.Now()}); !errors.Is(err, store.ErrCapacity) {
		t.Fatal("setup live records bypass lease bound", err)
	}
}

func TestLiveNativeTextBoundsAndStrictSignalUnion(t *testing.T) {
	part := &irc.PartEvent{Type: "part", Agent: "writer", Channel: "#room", Reason: strings.Repeat("x", 500) + "\tbye"}
	if got, err := normalizeLiveEvent(part, "#room", "reader"); err != nil || got.Reason != part.Reason {
		t.Fatal("valid native PART reason lost", got, err)
	}
	poll := irc.ChatEntry{Action: "results", Target: "#room", ID: strings.Repeat("a", 32), From: "writer", Text: strings.Repeat("q", 1000), Options: []string{"yes", "no"}, Votes: []int{0, 0}, ExpiresAt: time.Now().UTC()}
	body, err := json.Marshal(poll)
	if err != nil {
		t.Fatal(err)
	}
	raw := &irc.RawEvent{Type: "raw", Command: "780", Prefix: "server", Params: []string{"reader"}, Trailing: string(body)}
	if got, err := normalizeLiveEvent(raw, "#room", "reader"); err != nil || got.Signal == nil || got.Signal.Text != poll.Text {
		t.Fatal("valid native poll text lost", got, err)
	}
	for _, body := range []string{
		`{"action":"typing","target":"#room","from":"writer","expires_at":"2026-10-04T12:00:00Z","closed":false}`,
		`{"action":"prepare","target":"#room","from":"writer","id":"not-a-message","expires_at":"2026-10-04T12:00:00Z"}`,
		`{"action":"typing","target":"#room","from":"writer","expires_at":null}`,
		`{"action":"cancel","target":"#room","from":"writer","id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","expires_at":"2026-10-04T12:00:00Z","text":""}`,
	} {
		raw.Trailing = body
		if got, err := normalizeLiveEvent(raw, "#room", "reader"); got != nil || err == nil {
			t.Fatal("accepted malformed signal union", body, got, err)
		}
	}
	if got, err := normalizeLiveEvent(&irc.SignalEvent{Type: "signal", ChatEntry: poll}, "#room", "reader"); got != nil || err == nil {
		t.Fatal("typed signal bypassed strict wire decode", got, err)
	}
}
