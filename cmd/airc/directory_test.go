package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

func TestProfilesAndPresenceWorkAcrossOneShotConnections(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	mustCLI(t, address, "profile", "--nick", "Bob", "--model", "strong", "--workspace", "/work/code", "--tools", "go, shell", "--about", "I examine approaches", "--json")
	mustCLI(t, address, "profile", "--nick", "bob", "--about", "I review evidence", "--json")
	mustCLI(t, address, "presence", "--nick", "bob", "--set", "thinking", "--message", "Considering the empty-input failure", "--ttl", "1s", "--json")
	card := cliJSON[irc.DirectoryEvent](t, address, "directory", "--who", "BOB")
	if card.Model != "strong" || card.About != "I review evidence" || card.State != "thinking" || card.Connected || card.ExpiresAt.IsZero() {
		t.Fatalf("card = %+v", card)
	}
	before := card.LastSeen
	send(t, address, "bob", "#room", "I found the edge case")
	if card = cliJSON[irc.DirectoryEvent](t, address, "directory", "--who", "bob"); !card.LastSeen.After(before) {
		t.Fatalf("activity did not update last seen: %+v", card)
	}
	if got := mustCLI(t, address, "directory", "--json"); strings.Count(got, "\n") != 1 {
		t.Fatalf("temporary query nick leaked into directory: %s", got)
	}
	// A one-shot agent is listed as active, never as connected.
	if agents := cliJSON[[]irc.AgentInfo](t, address, "agents"); len(agents) != 1 || agents[0].Nick != "bob" || !agents[0].ConnectedAt.IsZero() || agents[0].ActiveAt.IsZero() || card.Connected {
		t.Fatalf("agents listed %+v", agents)
	}
	advanceServerClock(t, time.Until(card.ExpiresAt)+time.Second)
	card = cliJSON[irc.DirectoryEvent](t, address, "directory", "--who", "bob")
	if card.State != "unknown" || card.Note != "" || card.Model != "strong" {
		t.Fatalf("expired presence = %+v", card)
	}
	mustCLI(t, address, "presence", "--nick", "bob", "--set", "away", "--ttl", "5m", "--json")
	mustCLI(t, address, "presence", "--nick", "bob", "--clear", "--json")
	if card = cliJSON[irc.DirectoryEvent](t, address, "directory", "--who", "bob"); card.State != "unknown" || card.Model != "strong" || !card.ExpiresAt.IsZero() {
		t.Fatalf("clear = %+v", card)
	}
	mustCLI(t, address, "profile", "--nick", "bob", "--model", "", "--json")
	if card = cliJSON[irc.DirectoryEvent](t, address, "directory", "--who", "bob"); card.Model != "" || card.Tools != "go, shell" {
		t.Fatalf("field clear = %+v", card)
	}
	mustCLI(t, address, "profile", "--nick", "bob", "--clear", "--json")
	// Bob posted recently, so stays listed as active, without the profile.
	if card = cliJSON[irc.DirectoryEvent](t, address, "directory", "--json"); card.Nick != "bob" || card.AgentProfile != (irc.AgentProfile{}) || !card.Active {
		t.Fatalf("cleared profile remains: %+v", card)
	}
	advanceServerClock(t, 10*time.Minute)
	if got := mustCLI(t, address, "directory", "--json"); got != "" {
		t.Fatalf("an agent idle for ten minutes is still listed: %s", got)
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

func TestLacksCapabilityNamesTheFlagOrTheUpgrade(t *testing.T) {
	for capability, want := range map[string]string{
		"ADMIN":     "this aircd lacks ADMIN: it runs without --admin-token-file",
		"WAKE":      "this aircd lacks WAKE: it runs without --wake-allow (and an accounts file)",
		"DIRECTORY": "this aircd lacks DIRECTORY; upgrade when safe",
	} {
		if got := lacksCapability(capability); got != want {
			t.Errorf("lacksCapability(%s) = %q, want %q", capability, got, want)
		}
	}
	if got := hints(errors.New(lacksCapability("ADMIN"))); len(got) == 0 || !strings.Contains(got[0], "airc doctor") {
		t.Errorf("a configuration gap lost the doctor hint: %v", got)
	}
}
