package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// Every command refuses a daemon below this client's level at the dial, with
// one message and the non-retryable unsupported code, and uses a daemon above
// it, since the client takes nothing it does not know.
func TestCommandsRefuseADaemonBelowTheClientsLevel(t *testing.T) {
	agentEnv(t)
	id := strings.Repeat("a", 32)
	refusal := (&irc.DaemonTooOldError{Need: protocol.Level}).Error()
	for _, args := range [][]string{
		{"status", "--nick", "alice", "--channel", "#room", "--task", "tests", "--state", "working"},
		{"pins", "#room"},
		{"history", "#room"},
		{"send", "--nick", "alice", "--channel", "#room", "--message", "hi"},
		{"check", "--nick", "alice"},
		{"resolve", id, "--nick", "alice"},
	} {
		// olderDaemon serves one connection, so each command gets its own.
		old, closed := olderDaemon(t)
		_, stderr, err := cli(t, old, append(args, "--json")...)
		if !errors.Is(err, irc.ErrDaemonTooOld) {
			t.Errorf("%v against an older daemon: %v", args, err)
			continue
		}
		var failure commandFailure
		if json.Unmarshal([]byte(strings.TrimSpace(stderr)), &failure) != nil || failure.Code != "unsupported" || failure.Retryable || !strings.HasPrefix(failure.Message, refusal) || !strings.Contains(failure.Message, "never restart it yourself") {
			t.Errorf("%v against an older daemon: %s", args, stderr)
		}
		select {
		case <-closed:
		case <-time.After(time.Second):
			t.Errorf("%v left its connection to an older daemon open", args)
		}
	}
	for _, args := range [][]string{{"status", "--channel", "#room"}, {"charter", "#room"}, {"pins", "#room"}} {
		newer, _ := tokenDaemon(t, fmt.Sprintf("AIRC=%d HISTORY=8", protocol.Level+1), false)
		if _, _, err := cli(t, newer, args...); err != nil {
			t.Errorf("a daemon at level %d, %v: %v", protocol.Level+1, args, err)
		}
	}
}
