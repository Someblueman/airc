package main

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/internal/server"
)

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

func TestSearchTermsTimeWindowsAndNewestFirst(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	fails := sentMessage(t, address, "--nick", "alice", "--channel", "room", "--message", "solver fails on EMPTY input")
	output := sentMessage(t, address, "--nick", "bob", "--channel", "room", "--message", "empty solver output")
	passes := sentMessage(t, address, "--nick", "alice", "--channel", "room", "--message", "solver passes now")
	send(t, address, "bob", "#room", "an empty stub")

	got := historyIDs(t, mustCLI(t, address, "search", "solver empty", "--target", "#room", "--json"))
	if fmt.Sprint(got) != fmt.Sprint([]string{fails.ID, output.ID}) {
		t.Fatalf("terms = %v", got)
	}
	if out := mustCLI(t, address, "search", "solver empty", "--target", "#room", "--exact", "--json"); out != "" {
		t.Fatalf("--exact matched a literal substring nobody wrote: %s", out)
	}
	got = historyIDs(t, mustCLI(t, address, "search", `solver -"passes now"`, "--target", "#room", "--json"))
	if fmt.Sprint(got) != fmt.Sprint([]string{fails.ID, output.ID}) {
		t.Fatalf("exclusion = %v", got)
	}

	out, stderr, err := cli(t, address, "search", "solver", "--target", "#room", "--newest", "--limit", "2", "--json")
	if err != nil || fmt.Sprint(historyIDs(t, out)) != fmt.Sprint([]string{passes.ID, output.ID}) || !strings.Contains(stderr, "--after "+output.ID) {
		t.Fatalf("newest page = %s %s %v", out, stderr, err)
	}
	got = historyIDs(t, mustCLI(t, address, "search", "solver", "--target", "#room", "--newest", "--after", output.ID, "--json"))
	if fmt.Sprint(got) != fmt.Sprint([]string{fails.ID}) {
		t.Fatalf("newest continuation = %v", got)
	}

	if got = historyIDs(t, mustCLI(t, address, "search", "solver", "--target", "#room", "--since", "1h", "--json")); len(got) != 3 {
		t.Fatalf("--since 1h = %v", got)
	}
	if out := mustCLI(t, address, "search", "solver", "--target", "#room", "--until", "1h", "--json"); out != "" {
		t.Fatalf("--until 1h ago matched new messages: %s", out)
	}
	if got = historyIDs(t, mustCLI(t, address, "search", "solver", "--target", "#room", "--since", "2026-01-01", "--until", time.Now().Add(time.Hour).Format(time.RFC3339), "--json")); len(got) != 3 {
		t.Fatalf("date window = %v", got)
	}
	for _, args := range [][]string{
		{"--since", "yesterday"}, {"--since", "1h", "--until", "2h"}, {"--until", "-5m"},
	} {
		if _, _, err := cli(t, address, append([]string{"search", "solver", "--target", "#room"}, args...)...); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
}

// searchOnlyDaemon advertises SEARCH without SEARCH_TERMS and records the
// parameters of each SEARCH it is sent.
func searchOnlyDaemon(t *testing.T) (string, <-chan []string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	searches := make(chan []string, 4)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				scanner := bufio.NewScanner(conn)
				for scanner.Scan() {
					command, err := protocol.Parse(scanner.Text())
					if err != nil {
						return
					}
					switch command.Name {
					case "USER":
						fmt.Fprint(conn, ":server 766 observer :Ephemeral session\r\n:server 005 observer MULTILINE=1 SEARCH=1 :supported\r\n:server 001 observer :Welcome\r\n")
					case "SEARCH":
						searches <- command.Params
						fmt.Fprintf(conn, ":server 761 observer %s ok :End of search\r\n", command.Params[0])
					case "QUIT":
						return
					}
				}
			}()
		}
	}()
	t.Cleanup(func() { listener.Close() })
	return listener.Addr().String(), searches
}

func TestSearchOnADaemonWithoutTermsFallsBackToTheExactText(t *testing.T) {
	agentEnv(t)
	address, searches := searchOnlyDaemon(t)
	_, stderr, err := cli(t, address, "search", "two words", "--target", "#room")
	if err != nil || !strings.Contains(stderr, "predates SEARCH_TERMS") {
		t.Fatalf("multi-word search on an old daemon = %q %v", stderr, err)
	}
	if params := <-searches; len(params) != 4 {
		t.Fatalf("an old daemon was sent options: %q", params)
	}
	if _, stderr, err = cli(t, address, "search", "word", "--target", "#room"); err != nil || strings.Contains(stderr, "predates") {
		t.Fatalf("one word means the same either way: %q %v", stderr, err)
	}
	<-searches
	if _, _, err = cli(t, address, "search", "word", "--target", "#room", "--newest"); err == nil || !strings.Contains(err.Error(), "SEARCH_TERMS") {
		t.Fatalf("--newest on an old daemon = %v", err)
	}
}
