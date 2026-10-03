package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/pkg/irc"
)

func historyIDs(t *testing.T, text string) []string {
	t.Helper()
	var ids []string
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if line == "" {
			continue
		}
		var message irc.HistoryEvent
		if err := json.Unmarshal([]byte(line), &message); err != nil {
			t.Fatal(err)
		}
		if message.ID != "" {
			ids = append(ids, message.ID)
		}
	}
	return ids
}

func TestSearchReturnsOriginalMessagesAndPagesWithoutMovingCursors(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	root := sentMessage(t, address, "--nick", "alice", "--channel", "room", "--message", "Résumé: inspect solver.go")
	first := replyMessage(t, address, "bob", root.ID, "solver.go fails on empty input")
	send(t, address, "bob", "#room", "unrelated noise")
	dm := sentMessage(t, address, "--nick", "Bob", "--to", "alice", "--message", "solver.go reproduction\nempty input")
	child := replyMessage(t, address, "alice", first.ID, "solver.go fixed")
	out, stderr, err := cli(t, address, "search", "SOLVER.go", "--limit", "2", "--json")
	if err != nil || fmt.Sprint(historyIDs(t, out)) != fmt.Sprint([]string{root.ID, first.ID}) || !strings.Contains(stderr, first.ID) {
		t.Fatalf("first page = %s %s %v", out, stderr, err)
	}
	got := historyIDs(t, mustCLI(t, address, "search", "solver.go", "--after", first.ID, "--json"))
	if fmt.Sprint(got) != fmt.Sprint([]string{dm.ID, child.ID}) {
		t.Fatalf("continuation = %v", got)
	}
	got = historyIDs(t, mustCLI(t, address, "search", "solver.go", "--target", "thread:"+child.ID, "--from", "BOB", "--json"))
	if fmt.Sprint(got) != fmt.Sprint([]string{first.ID}) {
		t.Fatalf("conversation sender filter = %v", got)
	}
	got = historyIDs(t, mustCLI(t, address, "search", "RÉSUMÉ", "--target", "#room", "--json"))
	if fmt.Sprint(got) != fmt.Sprint([]string{root.ID}) {
		t.Fatalf("unicode search = %v", got)
	}
	t.Setenv("AIRC_CHANNEL", "#room")
	if got = historyIDs(t, mustCLI(t, address, "search", "solver.go", "--json")); len(got) != 3 {
		t.Fatalf("default room filter = %v", got)
	}
	if out = mustCLI(t, address, "search", "absent", "--json"); out != "" {
		t.Fatalf("empty search = %s", out)
	}
	got = historyIDs(t, mustCLI(t, address, "check", "--nick", "alice", "--channel", "room", "--json"))
	if len(got) != 3 {
		t.Fatalf("search consumed unread messages: %v", got)
	}
}

func TestSearchReportsExpiredCursorsAndNeverSearchesPrunedMessages(t *testing.T) {
	agentEnv(t)
	address := cliTestServerWith(t, server.Config{HistoryLimit: 2})
	old := sentMessage(t, address, "--nick", "alice", "--channel", "room", "--message", "pruned needle")
	send(t, address, "bob", "#room", "needle one")
	send(t, address, "bob", "#room", "needle two")
	if out, _, err := cli(t, address, "search", "needle", "--after", old.ID, "--json"); err == nil || !strings.Contains(err.Error(), "no longer retained") || out != "" {
		t.Fatalf("expired cursor = %s %v", out, err)
	}
	out := mustCLI(t, address, "search", "needle", "--json")
	if strings.Contains(out, old.ID) || len(historyIDs(t, out)) != 2 {
		t.Fatalf("pruned history searched: %s", out)
	}
}
