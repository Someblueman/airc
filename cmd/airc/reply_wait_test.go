package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestReplyWaitIgnoresOtherConversationsOwnRepliesAndNestedReplies(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	root := sentMessage(t, address, "--nick", "alice", "--channel", "room", "--message", "question")
	other := sentMessage(t, address, "--nick", "alice", "--channel", "room", "--message", "unrelated question")
	own := replyMessage(t, address, "alice", root.ID, "own follow-up")
	replyMessage(t, address, "bob", own.ID, "nested reply")
	type result struct {
		out string
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, _, err := cli(t, address, "check", "--nick", "alice", "--reply-to", root.ID, "--wait", "5s", "--json")
		done <- result{out, err}
	}()
	send(t, address, "bob", "#room", "@alice unrelated mention")
	mustCLI(t, address, "send", "--nick", "bob", "--to", "alice", "--message", "unrelated DM")
	replyMessage(t, address, "bob", other.ID, "answer to the other question")
	select {
	case got := <-done:
		t.Fatalf("wait returned for unrelated traffic: %+v", got)
	case <-time.After(150 * time.Millisecond):
	}
	expected := replyMessage(t, address, "bob", root.ID, "actual answer")
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatal(got.err)
		}
		messages := checkMessages(t, got.out)
		if len(messages) != 1 || messages[0].ID != expected.ID {
			t.Fatalf("wait result: %s", got.out)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("specific reply did not wake the wait")
	}
}

func TestReplyWaitReturnsAnAlreadyRetainedReplyAndTimesOutQuietly(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	root := sentMessage(t, address, "--nick", "alice", "--to", "bob", "--message", "question")
	replyMessage(t, address, "bob", root.ID, "already answered")
	start := time.Now()
	out := mustCLI(t, address, "check", "--nick", "alice", "--reply-to", root.ID, "--wait", "3s", "--json")
	if got := checkBodies(t, out); fmt.Sprint(got) != "[already answered]" || time.Since(start) > time.Second {
		t.Fatalf("retained reply = %v", got)
	}
	start = time.Now()
	out = mustCLI(t, address, "check", "--nick", "alice", "--reply-to", root.ID, "--wait", "150ms", "--json")
	if len(checkBodies(t, out)) != 0 || checkFooter(t, out).Code != "wait_expired" || time.Since(start) < 120*time.Millisecond {
		t.Fatalf("timeout = %q after %s", out, time.Since(start))
	}
}

func TestConversationCommandsRejectInvalidInputsAndOlderDaemons(t *testing.T) {
	agentEnv(t)
	id := strings.Repeat("a", 32)
	for _, args := range [][]string{
		{"send", "--nick", "alice", "--reply-to", "bad", "--message", "text"},
		{"send", "--nick", "alice", "--reply-to", id, "--channel", "room", "--message", "text"},
		{"check", "--nick", "alice", "--reply-to", id, "--mentions"},
		{"check", "--nick", "alice", "--reply-to", id, "--channel", "room"},
		{"thread", "bad"},
		{"thread", id, "extra"},
	} {
		if _, _, err := cli(t, "127.0.0.1:1", args...); err == nil || strings.Contains(err.Error(), "dial") {
			t.Fatalf("invalid args %v: %v", args, err)
		}
	}
	for _, args := range [][]string{
		{"send", "--nick", "alice", "--reply-to", id, "--message", "text"},
		{"check", "--nick", "alice", "--reply-to", id},
		{"thread", id},
	} {
		address, _ := olderDaemon(t, false)
		if _, _, err := cli(t, address, args...); err == nil || !strings.Contains(err.Error(), "REPLIES") {
			t.Fatalf("older daemon for %v: %v", args, err)
		}
	}
}

func TestReplyCursorCacheStaysBoundedAndPreservesOtherCursors(t *testing.T) {
	store := &cursorStore{}
	cursors := map[string]string{"#room": "room-cursor", "@alice": "inbox-cursor"}
	for i := range 200 {
		target := fmt.Sprintf("replies:%032x", i)
		cursors[target] = "reply-cursor"
		store.rememberReplyTarget(target, cursors)
	}
	if len(cursors) != 66 || len(store.ReplyOrder) != 64 || cursors["#room"] != "room-cursor" || cursors["@alice"] != "inbox-cursor" {
		t.Fatalf("unbounded or damaged cursors: %v", cursors)
	}
	if _, retained := cursors[fmt.Sprintf("replies:%032x", 0)]; retained {
		t.Fatal("old reply cursor was not evicted")
	}
}
