package backend

import (
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

func TestNormalizeLiveEventRoomChanges(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	tests := []struct {
		name  string
		event irc.Event
		want  LiveEvent
	}{
		{"join", &irc.JoinEvent{Type: "join", Agent: "Alice", Channel: "#Room", Timestamp: now}, LiveEvent{Kind: "join", Target: "#Room", Member: "Alice"}},
		{"part", &irc.PartEvent{Type: "part", Agent: "Alice", Channel: "#Room", Reason: "bye"}, LiveEvent{Kind: "part", Target: "#Room", Member: "Alice", Reason: "bye"}},
		{"quit", &irc.QuitEvent{Type: "quit", Agent: "Alice", Reason: "gone"}, LiveEvent{Kind: "quit", Target: "#Room", Member: "Alice", Reason: "gone"}},
		{"kick", &irc.KickEvent{Type: "kick", Channel: "#Room", Agent: "Alice", By: "Op", Reason: "quiet"}, LiveEvent{Kind: "kick", Target: "#Room", Actor: "Op", Member: "Alice", Reason: "quiet"}},
		{"topic", &irc.TopicEvent{Type: "topic", Channel: "#Room", Topic: "hello", SetBy: "Op"}, LiveEvent{Kind: "topic", Target: "#Room", Actor: "Op", Topic: stringPtr("hello")}},
		{"mode", &irc.RawEvent{Type: "raw", Command: "MODE", Prefix: "Op!user@host", Params: []string{"#Room", "+o", "Alice"}}, LiveEvent{Kind: "mode", Target: "#Room", Actor: "Op", Member: "Alice", Mode: "+o"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeLiveEvent(test.event, "#Room", "reader")
			if err != nil {
				t.Fatal(err)
			}
			if got == nil || got.Kind != test.want.Kind || got.Target != test.want.Target || got.Actor != test.want.Actor || got.Member != test.want.Member || got.Reason != test.want.Reason || got.Mode != test.want.Mode || test.want.Topic == nil && got.Topic != nil || test.want.Topic != nil && (got.Topic == nil || *got.Topic != *test.want.Topic) {
				t.Fatalf("got %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestNormalizeLiveEventSignalsStrictly(t *testing.T) {
	expires := time.Date(2026, 10, 4, 12, 0, 0, 123456789, time.FixedZone("offset", 3600))
	validTyping := `{"action":"typing","target":"#Room","from":"Alice","text":"working","expires_at":"2026-10-04T12:00:00.123456789+01:00"}`
	got, err := normalizeLiveEvent(&irc.RawEvent{Type: "raw", Command: "780", Prefix: "server", Params: []string{"reader"}, Trailing: validTyping}, "#Room", "reader")
	if err != nil || got == nil || got.Kind != "signal" || got.Actor != "Alice" || got.Signal == nil || !got.Signal.ExpiresAt.Equal(expires) {
		t.Fatalf("valid signal: %#v, %v", got, err)
	}

	prepare := irc.ChatEntry{Action: "prepare", Target: "#Room", ID: strings.Repeat("a", 32), From: "Alice", AccountID: strings.Repeat("b", 32), Text: "replying", ExpiresAt: time.Now().UTC().Add(time.Minute)}
	if live := normalizeSignal(prepare, "#Room"); live == nil || live.Signal == nil {
		t.Fatalf("prepare signal rejected: %#v", live)
	}
	results := irc.ChatEntry{Action: "results", Target: "#Room", ID: strings.Repeat("c", 32), From: "Alice", AccountID: strings.Repeat("d", 32), Text: "question", Options: []string{"yes", "no"}, Votes: []int{1, 0}, ExpiresAt: time.Now().UTC().Add(time.Minute)}
	if live := normalizeSignal(results, "#Room"); live == nil || live.Signal == nil {
		t.Fatalf("results signal rejected: %#v", live)
	}
}

func TestNormalizeLiveEventRejectsMalformedRelevantEvents(t *testing.T) {
	bad := []irc.Event{
		&irc.JoinEvent{Type: "join", Agent: "Alice", Channel: "#other", Timestamp: time.Now()},
		&irc.TopicEvent{Type: "topic", Channel: "#Room", Topic: "topic"},
		&irc.RawEvent{Type: "raw", Command: "MODE", Prefix: "Op!user@host", Params: []string{"#Room", "o", "Alice"}},
		&irc.RawEvent{Type: "raw", Command: "MODE", Prefix: "Op!user@host", Params: []string{"#Room", "+o", "Alice"}, Tags: map[string]string{"x": "y"}},
		&irc.RawEvent{Type: "raw", Command: "780", Prefix: "server", Params: []string{"other"}, Trailing: `{}`},
		&irc.RawEvent{Type: "raw", Command: "780", Prefix: "server", Params: []string{"reader"}, Trailing: `{"action":"typing","target":"#Room","from":"Alice","Action":"typing"}`},
		&irc.RawEvent{Type: "raw", Command: "780", Prefix: "server", Params: []string{"reader"}, Trailing: `{"action":"typing","target":"#Room","from":"Alice","target":null}`},
		&irc.RawEvent{Type: "raw", Command: "780", Prefix: "server", Params: []string{"reader"}, Trailing: `{"action":"typing","target":"#Room","from":"Alice","action":"typing"}`},
		&irc.RawEvent{Type: "raw", Command: "780", Prefix: "server", Params: []string{"reader"}, Trailing: `{"action":"typing","target":"#Room","from":"Alice"} trailing`},
	}
	for i, event := range bad {
		if got, err := normalizeLiveEvent(event, "#Room", "reader"); got != nil || err == nil {
			t.Errorf("case %d: got %#v, err %v", i, got, err)
		}
	}
	if got, err := normalizeLiveEvent(&irc.RawEvent{Type: "raw", Command: "001"}, "#Room", "reader"); got != nil || err != nil {
		t.Fatalf("registration noise: %#v, %v", got, err)
	}
}

func stringPtr(value string) *string { return &value }
