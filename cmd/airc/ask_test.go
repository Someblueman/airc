package main

import (
	"strings"
	"testing"
	"time"
)

// ask posts the question through send and waits for the first textual
// reply from someone else: a reaction does not end the wait, the reply row
// follows the receipt, and the reply is then marked read.
func TestAskWaitsForTheFirstTextualReply(t *testing.T) {
	agentEnv(t)
	tap := tapDaemon(t, cliTestServer(t))
	address := tap.addr
	type result struct {
		out string
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, _, err := cli(t, address, "ask", "--nick", "alice", "--channel", "room", "--message", "Does the parser handle empty input?", "--wait", "5s", "--run", "run-9", "--json")
		done <- result{out, err}
	}()
	tap.waitObserving(t, 1)
	question := historyIDs(t, mustCLI(t, address, "history", "#room", "--json"))
	if len(question) != 1 {
		t.Fatalf("the question was not posted: %v", question)
	}
	mustCLI(t, address, "react", question[0], "checking", "--nick", "bob")
	select {
	case got := <-done:
		t.Fatalf("a reaction ended the wait: %+v", got)
	case <-time.After(300 * time.Millisecond):
	}
	expected := replyMessage(t, address, "bob", question[0], "Yes, it returns an empty tree")
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatal(got.err)
		}
		lines := strings.Split(strings.TrimSpace(got.out), "\n")
		if len(lines) != 3 || !strings.Contains(lines[0], `"accepted":true`) || !strings.Contains(lines[0], `"id":"`+question[0]+`"`) || !strings.Contains(lines[0], `"run":"run-9"`) {
			t.Fatalf("receipt line: %v", lines)
		}
		messages := checkMessages(t, strings.Join(lines[1:], "\n"))
		if len(messages) != 1 || messages[0].ID != expected.ID || messages[0].ReplyTo != question[0] || messages[0].Reaction != "" {
			t.Fatalf("reply row: %s", got.out)
		}
		if footer := checkFooter(t, strings.Join(lines[1:], "\n")); footer.Code != "messages" {
			t.Fatalf("status: %+v", footer)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the reply did not wake the ask")
	}
	if again := checkBodies(t, mustCLI(t, address, "check", "--nick", "alice", "--reply-to", question[0], "--json")); len(again) != 0 {
		t.Fatalf("the reply was not marked read: %v", again)
	}
	// Without --json the receipt and the reply read as lines.
	out := mustCLI(t, address, "ask", "--nick", "alice", "--to", "bob", "--message", "Still there?", "--wait", "0")
	if !strings.HasPrefix(out, "asked bob [") || !strings.Contains(out, "]: Still there?\nno reply within 0s; airc check --reply-to ") {
		t.Fatalf("text form:\n%s", out)
	}
}

// An ask whose wait runs out prints the receipt and a wait_expired status;
// bad arguments are refused before anything is posted.
func TestAskReportsExpiryAndRefusesBadArguments(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	start := time.Now()
	out := mustCLI(t, address, "ask", "--nick", "alice", "--channel", "room", "--message", "anyone?", "--wait", "150ms", "--json")
	receipt, rest, _ := strings.Cut(out, "\n")
	if len(checkBodies(t, rest)) != 0 || checkFooter(t, rest).Code != "wait_expired" || !strings.Contains(receipt, `"accepted":true`) || time.Since(start) < 120*time.Millisecond {
		t.Fatalf("expiry = %q after %s", out, time.Since(start))
	}
	if ids := historyIDs(t, mustCLI(t, address, "history", "#room", "--json")); len(ids) != 1 {
		t.Fatalf("the question should be posted once: %v", ids)
	}
	for _, args := range [][]string{
		{"ask", "--nick", "alice", "--message", "no target"},
		{"ask", "--nick", "alice", "--channel", "room", "--to", "bob", "--message", "two targets"},
		{"ask", "--nick", "alice", "--channel", "room", "--message", ""},
		{"ask", "--nick", "alice", "--channel", "room", "--message", "x", "--wait", "2h"},
		{"ask", "--nick", "alice", "--channel", "room", "--message", "x", "--run", "a/b"},
		{"ask", "--nick", "alice", "--channel", "room", "--message", "x", "extra"},
	} {
		if _, _, err := cli(t, "127.0.0.1:1", args...); err == nil || strings.Contains(err.Error(), "dial") {
			t.Fatalf("invalid args %v: %v", args, err)
		}
	}
}
