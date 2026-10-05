package irc_test

import (
	"testing"

	"github.com/Someblueman/airc/pkg/irc"
)

func TestAgentsRawPreservesEnvelopeAndRestoresTypedEvents(t *testing.T) {
	_, listener := startServer(t, "127.0.0.1:0")
	observer := newClient(t, "agents-observer", listener.Addr().String())
	peer := newClient(t, "agents-peer", listener.Addr().String())
	defer peer.Close()

	if err := observer.AgentsRaw(); err != nil {
		t.Fatal(err)
	}
	raw := nextEvent(t, observer, func(event irc.Event) bool {
		value, ok := event.(*irc.RawEvent)
		return ok && value.Command == "763"
	})
	value := raw.(*irc.RawEvent)
	if len(value.Params) != 1 || value.Params[0] != observer.Nick() || value.Trailing == "" {
		t.Fatalf("raw AGENTS record=%+v", value)
	}
	end := nextEvent(t, observer, func(event irc.Event) bool {
		value, ok := event.(*irc.RawEvent)
		return ok && value.Command == "764"
	})
	if value := end.(*irc.RawEvent); len(value.Params) != 1 || value.Params[0] != observer.Nick() || value.Trailing != "End of agents list" {
		t.Fatalf("raw AGENTS terminator=%+v", value)
	}

	if err := observer.Agents(); err != nil {
		t.Fatal(err)
	}
	typed := nextEvent(t, observer, func(event irc.Event) bool {
		_, agent := event.(*irc.AgentsEvent)
		_, done := event.(*irc.EndOfAgentsEvent)
		return agent || done
	})
	if _, ok := typed.(*irc.AgentsEvent); !ok {
		t.Fatalf("typed AGENTS record=%T", typed)
	}
}
