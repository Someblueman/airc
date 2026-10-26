package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Someblueman/airc/pkg/irc"
)

// --run and AIRC_RUN label sends, replies, statuses, handoffs and
// decisions; check, history and thread rows carry the label, and search
// and history filter by it.
func TestRunLabelsPostsAndFiltersHistoryAndSearch(t *testing.T) {
	agentEnv(t)
	address := chatServer(t, 64)
	first := sentMessage(t, address, "--nick", "alice", "--channel", "room", "--message", "first pass", "--run", "run-1")
	if first.Run != "run-1" {
		t.Fatalf("receipt %+v", first)
	}
	t.Setenv("AIRC_RUN", "run-2")
	second := sentMessage(t, address, "--nick", "alice", "--channel", "room", "--message", "second pass")
	reply := replyMessage(t, address, "bob", first.ID, "reply in the second run")
	if second.Run != "run-2" || reply.Run != "run-2" {
		t.Fatalf("AIRC_RUN default: %+v %+v", second, reply)
	}
	t.Setenv("AIRC_RUN", "")
	plain := sentMessage(t, address, "--nick", "alice", "--channel", "room", "--message", "no run")
	if plain.Run != "" {
		t.Fatalf("an unlabelled send carries %q", plain.Run)
	}
	if out := mustCLI(t, address, "status", "--nick", "alice", "--channel", "room", "--task", "port the fix", "--state", "working", "--run", "run-1", "--json"); !strings.Contains(out, `"run":"run-1"`) {
		t.Fatalf("status row: %s", out)
	}
	if out := mustCLI(t, address, "handoff", "--nick", "alice", "--channel", "room", "--message", "take it", "--run", "run-1", "--json"); !strings.Contains(out, `"run":"run-1"`) {
		t.Fatalf("handoff row: %s", out)
	}
	if out := mustCLI(t, address, "decide", "--nick", "alice", "--channel", "room", "--from", "bob", "--message", "ship?", "--run", "run-1", "--json"); !strings.Contains(out, `"run":"run-1"`) {
		t.Fatalf("decision row: %s", out)
	}

	rows := checkMessages(t, mustCLI(t, address, "check", "--nick", "reader", "--channel", "room", "--json"))
	runs := map[string]string{}
	for _, row := range rows {
		runs[row.Message] = row.Run
	}
	if runs["first pass"] != "run-1" || runs["second pass"] != "run-2" || runs["reply in the second run"] != "run-2" || runs["no run"] != "" {
		t.Fatalf("check rows carry %v", runs)
	}
	ids := func(out string) []string { return historyIDs(t, out) }
	if got := ids(mustCLI(t, address, "history", "#room", "--run", "run-2", "--json")); fmt.Sprint(got) != fmt.Sprint([]string{second.ID, reply.ID}) {
		t.Fatalf("history --run = %v", got)
	}
	if got := ids(mustCLI(t, address, "search", "pass", "--target", "#room", "--run", "run-1", "--json")); fmt.Sprint(got) != fmt.Sprint([]string{first.ID}) {
		t.Fatalf("search --run = %v", got)
	}
	if got := ids(mustCLI(t, address, "search", "pass", "--target", "*", "--run", "run-2", "--json")); fmt.Sprint(got) != fmt.Sprint([]string{second.ID}) {
		t.Fatalf("search --run across targets = %v", got)
	}
	thread := jsonLines[irc.HistoryEvent](t, mustCLI(t, address, "thread", first.ID, "--json"))
	if len(thread) != 2 || thread[0].Run != "run-1" || thread[1].Run != "run-2" {
		t.Fatalf("thread rows %+v", thread)
	}

	// A bad label is refused before connecting; an older daemon refuses
	// the filters and takes a labelled send without the label.
	for _, args := range [][]string{
		{"send", "--nick", "alice", "--channel", "room", "--message", "x", "--run", strings.Repeat("r", 65)},
		{"send", "--nick", "alice", "--channel", "room", "--message", "x", "--run", "a/b"},
		{"history", "#room", "--run", "a b"},
		{"search", "x", "--run", "" + strings.Repeat("r", 65)},
	} {
		if _, _, err := cli(t, "127.0.0.1:1", args...); err == nil || strings.Contains(err.Error(), "dial") {
			t.Fatalf("invalid args %v: %v", args, err)
		}
	}
	// The stub daemon lacks SEARCH as well as RUN; history reaches the RUN check.
	for _, args := range [][]string{{"history", "#room", "--run", "run-1"}, {"search", "x", "--target", "#room", "--run", "run-1"}} {
		older, _ := olderDaemon(t, false)
		if _, _, err := cli(t, older, args...); err == nil || !strings.Contains(err.Error(), "RUN") && !strings.Contains(err.Error(), "lacks SEARCH") {
			t.Fatalf("older daemon for %v: %v", args, err)
		}
	}
}
