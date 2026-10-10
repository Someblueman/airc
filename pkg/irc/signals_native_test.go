package irc_test

import (
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestObserveSignalsRawUsesNative780AndTypedModeCanBeRestored(t *testing.T) {
	address := startServer(t, server.Config{HistoryLimit: 32}, nil)
	observer := newClient(t, "observer", address)
	writer := newClient(t, "writer", address)
	for _, room := range []string{"#native-signal", "#native-signal-typed"} {
		if err := writer.Join(room); err != nil {
			t.Fatal(err)
		}
	}
	nextEvent(t, writer, func(event irc.Event) bool {
		v, ok := event.(*irc.JoinEvent)
		return ok && v.Channel == "#native-signal-typed"
	})
	if err := observer.ObserveSignalsRaw("#native-signal"); err != nil {
		t.Fatal(err)
	}
	nextEvent(t, observer, func(event irc.Event) bool {
		v, ok := event.(*irc.RawEvent)
		return ok && v.Command == "765" && len(v.Params) == 2 && v.Params[1] == "#native-signal"
	})
	if err := writer.Raw(`CHAT :{"action":"typing","target":"#native-signal","seconds":5}`); err != nil {
		t.Fatal(err)
	}
	raw := nextEvent(t, observer, func(event irc.Event) bool {
		value, ok := event.(*irc.RawEvent)
		return ok && value.Command == "780"
	}).(*irc.RawEvent)
	if raw.Prefix != "server" || len(raw.Params) != 1 || raw.Params[0] != "observer" || !strings.Contains(raw.Trailing, `"action":"typing"`) || !strings.Contains(raw.Trailing, `"from":"writer"`) {
		t.Fatalf("unexpected native raw signal: %#v", raw)
	}
	if err := observer.Observe("#native-signal-typed"); err != nil {
		t.Fatal(err)
	}
	nextEvent(t, observer, func(event irc.Event) bool {
		v, ok := event.(*irc.RawEvent)
		return ok && v.Command == "765" && len(v.Params) == 2 && v.Params[1] == "#native-signal-typed"
	})
	if err := writer.Raw(`CHAT :{"action":"thinking","target":"#native-signal-typed","seconds":5,"text":"working"}`); err != nil {
		t.Fatal(err)
	}
	typed := nextEvent(t, observer, func(event irc.Event) bool {
		value, ok := event.(*irc.SignalEvent)
		return ok && value.ChatEntry.Action == "thinking"
	}).(*irc.SignalEvent)
	if typed.ChatEntry.Target != "#native-signal-typed" || typed.ChatEntry.Text != "working" {
		t.Fatalf("unexpected native typed signal: %#v", typed)
	}
}
