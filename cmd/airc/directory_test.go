package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

func readCard(t *testing.T, address, nick string) irc.DirectoryEvent {
	t.Helper()
	var card irc.DirectoryEvent
	if err := json.Unmarshal([]byte(mustCLI(t, address, "directory", "--who", nick, "--json")), &card); err != nil {
		t.Fatal(err)
	}
	return card
}

func TestProfilesAndPresenceWorkAcrossOneShotConnections(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	mustCLI(t, address, "profile", "--nick", "Bob", "--model", "strong", "--workspace", "/work/code", "--tools", "go, shell", "--about", "I examine approaches", "--json")
	mustCLI(t, address, "profile", "--nick", "bob", "--about", "I review evidence", "--json")
	mustCLI(t, address, "presence", "--nick", "bob", "--set", "thinking", "--message", "Considering the empty-input failure", "--ttl", "1s", "--json")
	card := readCard(t, address, "BOB")
	if card.Model != "strong" || card.About != "I review evidence" || card.State != "thinking" || card.Connected || card.ExpiresAt.IsZero() {
		t.Fatalf("card = %+v", card)
	}
	before := card.LastSeen
	send(t, address, "bob", "#room", "I found the edge case")
	if card = readCard(t, address, "bob"); !card.LastSeen.After(before) {
		t.Fatalf("activity did not update last seen: %+v", card)
	}
	if got := mustCLI(t, address, "directory", "--json"); strings.Count(got, "\n") != 1 {
		t.Fatalf("temporary query nick leaked into directory: %s", got)
	}
	if got := mustCLI(t, address, "agents", "--json"); strings.TrimSpace(got) != "[]" {
		t.Fatalf("one-shot profiles changed connected AGENTS contract: %s", got)
	}
	time.Sleep(time.Until(card.ExpiresAt) + 20*time.Millisecond)
	if card = readCard(t, address, "bob"); card.State != "unknown" || card.Note != "" || card.Model != "strong" {
		t.Fatalf("expired presence = %+v", card)
	}
	mustCLI(t, address, "presence", "--nick", "bob", "--set", "away", "--ttl", "5m", "--json")
	mustCLI(t, address, "presence", "--nick", "bob", "--clear", "--json")
	if card = readCard(t, address, "bob"); card.State != "unknown" || card.Model != "strong" || !card.ExpiresAt.IsZero() {
		t.Fatalf("clear = %+v", card)
	}
	mustCLI(t, address, "profile", "--nick", "bob", "--model", "", "--json")
	if card = readCard(t, address, "bob"); card.Model != "" || card.Tools != "go, shell" {
		t.Fatalf("field clear = %+v", card)
	}
	mustCLI(t, address, "profile", "--nick", "bob", "--clear", "--json")
	if got := mustCLI(t, address, "directory", "--json"); got != "" {
		t.Fatalf("cleared profile remains: %s", got)
	}
}

func TestChatCommandsRejectInvalidUpdatesAndOlderDaemons(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	for _, args := range [][]string{
		{"presence", "--nick", "alice", "--set", "busy"},
		{"presence", "--nick", "alice", "--set", "thinking", "--ttl", "24h"},
		{"profile", "--nick", "alice", "--about", strings.Repeat("x", 401)},
		{"profile", "--nick", "alice", "--tools", "a\nb"},
		{"presence", "--nick", "alice", "--clear", "--set", "away"},
		{"profile", "--nick", "alice", "--who", "bob", "--model", "overwrite"},
		{"directory", "--clear"},
	} {
		if _, _, err := cli(t, address, args...); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	if got := mustCLI(t, address, "directory", "--json"); got != "" {
		t.Fatalf("invalid updates created cards: %s", got)
	}
	for _, args := range [][]string{
		{"directory"}, {"profile", "--nick", "alice", "--about", "reviewer"},
		{"presence", "--nick", "alice", "--set", "thinking"},
		{"search", "needle"}, {"react", strings.Repeat("a", 32), "seen", "--nick", "alice"},
	} {
		old, _ := olderDaemon(t, false)
		if _, _, err := cli(t, old, args...); err == nil || !strings.Contains(err.Error(), "DIRECTORY") && !strings.Contains(err.Error(), "SEARCH") && !strings.Contains(err.Error(), "REACTIONS") {
			t.Fatalf("old daemon accepted %v: %v", args, err)
		}
	}
}
