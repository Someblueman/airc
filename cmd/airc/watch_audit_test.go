package main

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestAllDMWatchCatchesUpAfterAnInitiallyEmptyHistory(t *testing.T) {
	agentEnv(t)
	old := watchMinBackoff
	watchMinBackoff = 300 * time.Millisecond
	t.Cleanup(func() { watchMinBackoff = old })
	file := filepath.Join(t.TempDir(), "history.jsonl")
	cfg := server.Config{HistoryLimit: 64}
	first, address := startServerAt(t, "127.0.0.1:0", cfg, file)
	send(t, address, "planner", "#ops", "anchor")
	// The channel's anchor marks that startup has completed; audit starts empty.
	out, stop := startWatch(t, "--all-dms", "--channel", "ops", "--addr", address, "--backlog", "10", "--color", "never")
	waitForOutput(t, out, "anchor")
	first.stop(t)
	waitForOutput(t, out, "connection lost")
	_, _ = startServerAt(t, address, cfg, file)
	mustCLI(t, address, "send", "--nick", "planner", "--to", "muse", "--message", "DM during outage")
	waitForOutput(t, out, "DM during outage")
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); strings.Count(got, "DM during outage") != 1 || !strings.Contains(got, "[dm -> muse]") {
		t.Fatalf("wrong audit catch-up: %s", got)
	}
}

func TestAuditCommandsRejectAnOlderDaemonClearly(t *testing.T) {
	agentEnv(t)
	address, _ := olderDaemon(t, false)
	err := runWatchContext(context.Background(), []string{"--all-dms", "--addr", address}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "DM_AUDIT") {
		t.Fatalf("audit watcher against older daemon = %v", err)
	}
	address, _ = olderDaemon(t, false)
	_, _, err = cli(t, address, "history", irc.AllDirectMessages)
	if err == nil || !strings.Contains(err.Error(), "DM_AUDIT") {
		t.Fatalf("audit history against older daemon = %v", err)
	}
}
