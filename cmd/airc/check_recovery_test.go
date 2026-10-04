package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/server"
)

func TestFirstInboxReadsAllRetainedAssignmentsInBoundedPages(t *testing.T) {
	agentEnv(t)
	address := cliTestServerWith(t, server.Config{HistoryLimit: 64})
	for i := range 25 {
		mustCLI(t, address, "send", "--nick", "planner", "--to", "me", "--message", fmt.Sprint(i))
	}
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
	for i := range 15 {
		send(t, address, "writer", "#room", fmt.Sprint(i))
	}
	out := mustCLI(t, address, "check", "--nick", "me", "--channel", "room", "--limit", "2", "--max-messages", "3", "--json")
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
