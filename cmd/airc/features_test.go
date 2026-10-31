package main

import (
	"strings"
	"testing"
)

// A daemon from before AIRC levels advertised CHAT=1 for pins, polls and
// room settings, so those still go through; statuses, resolutions and
// charters came later with no token of their own, so the client refuses them
// by name instead of letting the daemon answer "unknown CHAT action".
func TestChatActionsAreGatedOnTheirOwnFeature(t *testing.T) {
	agentEnv(t)
	id := strings.Repeat("a", 32)
	// tokenDaemon serves one connection, so each command gets its own.
	for want, args := range map[string][]string{
		"STATUS_POSTS": {"status", "--nick", "alice", "--channel", "#room", "--task", "tests", "--state", "working"},
		"RESOLVE":      {"resolve", id, "--nick", "alice"},
		"CHARTER":      {"charter", "#room"},
		"CHARTER ":     {"room", "#room", "--nick", "alice", "--charter", "", "--as-operator"},
	} {
		old, _ := tokenDaemon(t, "CHAT=1", false)
		want = strings.TrimSpace(want)
		if _, _, err := cli(t, old, args...); err == nil || err.Error() != lacksCapability(want) {
			t.Errorf("CHAT=1 daemon, %v: %v, want %q", args, err, lacksCapability(want))
		}
	}
	for _, args := range [][]string{{"pins", "#room"}, {"room", "#room"}, {"poll-results", id}} {
		old, _ := tokenDaemon(t, "CHAT=1", false)
		if _, _, err := cli(t, old, args...); err != nil {
			t.Errorf("CHAT=1 daemon, %v: %v", args, err)
		}
	}
	// With the level, nothing is gated on CHAT by proxy any more.
	for _, args := range [][]string{{"status", "--channel", "#room"}, {"charter", "#room"}, {"pins", "#room"}} {
		current, _ := tokenDaemon(t, "AIRC=1 HISTORY=8", false)
		if _, _, err := cli(t, current, args...); err != nil {
			t.Errorf("level-1 daemon, %v: %v", args, err)
		}
	}
}
