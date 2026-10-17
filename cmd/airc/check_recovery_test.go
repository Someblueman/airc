package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/server"
)

func TestFirstInboxReadsAllRetainedAssignmentsInBoundedPages(t *testing.T) {
	agentEnv(t)
	address := cliTestServerWith(t, server.Config{HistoryLimit: 64})
	messages := make([]seedMessage, 25)
	for i := range messages {
		messages[i] = seedMessage{target: "me", body: fmt.Sprint(i)}
	}
	seedNativeMessages(t, address, "planner", messages)
	var got []string
	for range 4 {
		got = append(got, checkBodies(t, mustCLI(t, address, "check", "--nick", "me", "--max-messages", "7", "--initial", "1", "--json"))...)
	}
	if len(got) != 25 || got[0] != "0" || got[24] != "24" {
		t.Fatalf("first inbox lost assignments: %v", got)
	}
}

func TestExpiredCursorRecoversOldestRetainedMessagesAndReportsGap(t *testing.T) {
	agentEnv(t)
	address := cliTestServerWith(t, server.Config{HistoryLimit: 10})
	send(t, address, "writer", "#room", "before")
	mustCLI(t, address, "check", "--nick", "me", "--channel", "room")
	messages := make([]seedMessage, 15)
	for i := range messages {
		messages[i] = seedMessage{target: "#room", body: fmt.Sprint(i)}
	}
	seedNativeMessages(t, address, "writer", messages)
	out, stderr, err := cli(t, address, "check", "--nick", "me", "--channel", "room", "--limit", "2", "--max-messages", "3", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "may have been missed") {
		t.Fatalf("no expiry warning on stderr: %q", stderr)
	}
	if got := checkBodies(t, out); fmt.Sprint(got) != "[5 6 7]" {
		t.Fatalf("recovery skipped retained messages: %v", got)
	}
	status := checkFooter(t, out)
	if !status.More || !strings.Contains(strings.Join(status.Gaps, ","), "#room") {
		t.Fatalf("gap is not machine-readable: %s", out)
	}
	var got []string
	for range 3 {
		got = append(got, checkBodies(t, mustCLI(t, address, "check", "--nick", "me", "--channel", "room", "--max-messages", "3", "--json"))...)
	}
	if fmt.Sprint(got) != "[8 9 10 11 12 13 14]" {
		t.Fatalf("recovery continuation = %v", got)
	}
	if out := mustCLI(t, address, "check", "--nick", "me", "--channel", "room", "--json"); len(checkBodies(t, out)) != 0 || checkFooter(t, out).Code != "no_messages" {
		t.Fatalf("cursor was not repaired after expiry: %s", out)
	}
}

func TestCheckFailsClearlyWhenHistoryIsDisabled(t *testing.T) {
	agentEnv(t)
	address := cliTestServerWith(t, server.Config{})
	_, _, err := cli(t, address, "check", "--nick", "me")
	if err == nil || !strings.Contains(err.Error(), "history is disabled") {
		t.Fatalf("disabled history error = %v", err)
	}
}

func TestSendCheckUsesBoundedInboxAndDoesNotEchoItsOwnPost(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	send(t, address, "writer", "#room", "ready")
	out := mustCLI(t, address, "send", "--nick", "me", "--channel", "room", "--message", "status", "--check", "--json")
	if got := checkBodies(t, out); fmt.Sprint(got) != "[status ready]" {
		t.Fatalf("combined send/check = %v", got)
	}
	if got := checkBodies(t, mustCLI(t, address, "check", "--nick", "me", "--channel", "room", "--json")); len(got) != 0 {
		t.Fatalf("combined check did not save cursors: %v", got)
	}
}

// A cursor older than the window resumes from the archive, which reads a
// bounded amount per request. Pages of the agent's own messages show
// nothing, but each check must still move the cursor on, so the next one
// reaches the reply instead of reading the same stretch again.
func TestCheckProgressesThroughArchivedHistoryWithNothingToShow(t *testing.T) {
	agentEnv(t)
	path := filepath.Join(t.TempDir(), "history.jsonl")
	address := cliTestServerSetup(t, server.Config{HistoryLimit: 3, HistorySegmentBytes: 2048}, func(s *server.Server) error {
		return s.RestoreHistory(path)
	})
	send(t, address, "writer", "#room", "before")
	if got := checkBodies(t, mustCLI(t, address, "check", "--nick", "me", "--channel", "room", "--json")); fmt.Sprint(got) != "[before]" {
		t.Fatalf("first check: %v", got)
	}
	own := make([]seedMessage, 200)
	for i := range own {
		own[i] = seedMessage{target: "#room", body: fmt.Sprint("own ", i)}
	}
	seedNativeMessages(t, address, "me", own)
	send(t, address, "writer", "#room", "after")
	var got []string
	for range 100 {
		out := mustCLI(t, address, "check", "--nick", "me", "--channel", "room", "--json")
		got = append(got, checkBodies(t, out)...)
		if !checkFooter(t, out).More {
			break
		}
	}
	if fmt.Sprint(got) != "[after]" {
		t.Fatalf("checks through the archive showed %v", got)
	}
}
