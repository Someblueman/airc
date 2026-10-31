package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestAllDMWatchShowsBacklogAndLiveTrafficOnlyOnce(t *testing.T) {
	agentEnv(t)
	address := cliTestServerWith(t, server.Config{HistoryLimit: 64})
	mustCLI(t, address, "send", "--nick", "planner", "--to", "muse", "--message", "retained DM")
	out, stop := startWatch(t, "--all-dms", "--channel", "@muse", "--addr", address, "--json", "--backlog", "10")
	waitForOutput(t, out, "retained DM")
	mustCLI(t, address, "send", "--nick", "muse", "--to", "planner", "--message", "live DM")
	send(t, address, "planner", "#ops", "unrelated public traffic")
	waitForOutput(t, out, "live DM")
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); strings.Count(got, "retained DM") != 1 || strings.Count(got, "live DM") != 1 || strings.Contains(got, "unrelated public traffic") {
		t.Fatalf("wrong DM audit stream: %s", got)
	}
}

// A watcher and a history read refuse a daemon below this client's level
// at the dial: the watcher does not start retrying against it.
func TestWatchAndHistoryRefuseADaemonBelowTheClientsLevel(t *testing.T) {
	agentEnv(t)
	address, _ := olderDaemon(t)
	err := runWatchContext(context.Background(), []string{"--all-dms", "--addr", address}, io.Discard, io.Discard)
	if !errors.Is(err, irc.ErrDaemonTooOld) {
		t.Fatalf("audit watcher against an older daemon = %v", err)
	}
	address, _ = olderDaemon(t)
	_, _, err = cli(t, address, "history", irc.AllDirectMessages)
	if !errors.Is(err, irc.ErrDaemonTooOld) {
		t.Fatalf("audit history against an older daemon = %v", err)
	}
}
