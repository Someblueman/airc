package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestHistoryAfterPagesServerSideAndReadsDirectMessages(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	for i := 1; i <= 5; i++ {
		send(t, address, "writer", "#room", fmt.Sprint("h", i))
	}
	// Page forward from the oldest message: h2 and h3 follow h1, and more remain.
	cursor := historyIDs(t, mustCLI(t, address, "history", "#room", "--json"))[0]
	stdout, stderr, err := cli(t, address, "history", "#room", "--after", cursor, "--limit", "2", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSpace(stdout), "\n"); len(lines) != 2 || !strings.Contains(lines[0], `"h2"`) {
		t.Fatalf("page = %q", stdout)
	}
	if !strings.Contains(stderr, "--after") {
		t.Fatalf("no continuation hint: %q", stderr)
	}
	mustCLI(t, address, "send", "--nick", "planner", "--to", "Builder", "--message", "dm")
	if out := mustCLI(t, address, "history", "builder"); !strings.Contains(out, "planner: dm") {
		t.Fatalf("inbox history = %q", out)
	}
}
