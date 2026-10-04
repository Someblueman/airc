package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/pkg/irc"
)

func sentMessage(t *testing.T, address string, args ...string) irc.MessageEvent {
	t.Helper()
	var message irc.MessageEvent
	out := mustCLI(t, address, append([]string{"send"}, append(args, "--json")...)...)
	if err := json.Unmarshal([]byte(out), &message); err != nil {
		t.Fatal(err)
	}
	return message
}

func replyMessage(t *testing.T, address, nick, parent, body string) irc.MessageEvent {
	t.Helper()
	return sentMessage(t, address, "--nick", nick, "--reply-to", parent, "--message", body)
}

func TestConversationLinksRepliesAndPagesWithoutUnrelatedMessages(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	root := sentMessage(t, address, "--nick", "alice", "--channel", "room", "--message", "question")
	other := sentMessage(t, address, "--nick", "alice", "--channel", "room", "--message", "another question")
	t.Setenv("AIRC_CHANNEL", "#wrong") // reply chooses the parent's destination
	first := replyMessage(t, address, "bob", root.ID, "first\nanswer")
	child := replyMessage(t, address, "alice", first.ID, "follow-up")
	last := replyMessage(t, address, "bob", root.ID, "second answer")
	for _, reply := range []irc.MessageEvent{first, child, last} {
		if reply.ThreadID != root.ID || reply.Target != "#room" {
			t.Fatalf("wrong conversation: %+v", reply)
		}
	}
	if first.ReplyTo != root.ID || child.ReplyTo != first.ID || last.ReplyTo != root.ID || root.ReplyTo != "" {
		t.Fatal("reply parent links were lost")
	}
	replyMessage(t, address, "bob", other.ID, "unrelated answer")
	for _, id := range []string{root.ID, child.ID} {
		out := mustCLI(t, address, "thread", id, "--json")
		var got []string
		for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
			var message irc.HistoryEvent
			if err := json.Unmarshal([]byte(line), &message); err != nil {
				t.Fatal(err)
			}
			got = append(got, message.ID)
		}
		if fmt.Sprint(got) != fmt.Sprint([]string{root.ID, first.ID, child.ID, last.ID}) {
			t.Fatalf("conversation from %s = %v", id, got)
		}
	}
	out, stderr, err := cli(t, address, "thread", root.ID, "--limit", "2", "--json")
	if err != nil || strings.Count(out, "\n") != 2 || !strings.Contains(stderr, first.ID) {
		t.Fatalf("first page: %q %q %v", out, stderr, err)
	}
	out = mustCLI(t, address, "thread", root.ID, "--after", first.ID, "--json")
	if strings.Count(out, "\n") != 2 || !strings.Contains(out, child.ID) || !strings.Contains(out, last.ID) {
		t.Fatalf("continuation = %s", out)
	}
	// Thread reads do not consume ordinary checks.
	if got := checkBodies(t, mustCLI(t, address, "check", "--nick", "bob", "--channel", "room", "--json")); len(got) != 3 {
		t.Fatalf("thread read consumed room messages: %v", got)
	}
}

func TestReplyChecksUseIndependentBoundedCursors(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	root := sentMessage(t, address, "--nick", "alice", "--channel", "room", "--message", "question")
	for i := range 3 {
		replyMessage(t, address, "bob", root.ID, fmt.Sprint(i))
	}
	send(t, address, "bob", "#room", "room chatter")
	mustCLI(t, address, "send", "--nick", "bob", "--to", "alice", "--message", "unrelated DM")
	check := func(extra ...string) string {
		return mustCLI(t, address, append([]string{"check", "--nick", "alice", "--reply-to", root.ID, "--max-messages", "1", "--json"}, extra...)...)
	}
	if got := checkBodies(t, check("--peek")); fmt.Sprint(got) != "[0]" {
		t.Fatalf("peek = %v", got)
	}
	for i := range 3 {
		out := check()
		if got := checkBodies(t, out); fmt.Sprint(got) != fmt.Sprint([]string{fmt.Sprint(i)}) {
			t.Fatalf("reply page = %v", got)
		}
		if i < 2 && !checkFooter(t, out).More {
			t.Fatalf("missing continuation: %s", out)
		}
		m := checkMessages(t, out)[0]
		if m.ReplyTo != root.ID || m.ThreadID != root.ID {
			t.Fatalf("check lost links: %+v", m)
		}
	}
	if out := check(); len(checkBodies(t, out)) != 0 || checkFooter(t, out).Code != "no_messages" {
		t.Fatalf("reply repeated: %s", out)
	}
	got := checkBodies(t, mustCLI(t, address, "check", "--nick", "alice", "--channel", "room", "--json"))
	if fmt.Sprint(got) != "[0 1 2 room chatter unrelated DM]" {
		t.Fatalf("reply checks consumed ordinary messages: %v", got)
	}
}

func TestDMRepliesRouteBothDirectionsAndRejectThirdParticipants(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	root := sentMessage(t, address, "--nick", "alice", "--to", "Bob", "--message", "private question")
	first := replyMessage(t, address, "bob", root.ID, "private answer")
	second := replyMessage(t, address, "Alice", first.ID, "thanks")
	if first.Target != "alice" || second.Target != "bob" || second.ThreadID != root.ID {
		t.Fatalf("DM routing: %+v %+v", first, second)
	}
	if _, _, err := cli(t, address, "send", "--nick", "carol", "--reply-to", root.ID, "--message", "intrusion"); err == nil || !strings.Contains(err.Error(), "participants") {
		t.Fatalf("third participant: %v", err)
	}
	out := mustCLI(t, address, "thread", second.ID, "--json")
	if strings.Count(out, "\n") != 3 || strings.Contains(out, "intrusion") {
		t.Fatalf("DM conversation: %s", out)
	}
	if out := mustCLI(t, address, "history", "#room", "--json"); out != "" {
		t.Fatalf("DM leaked to room: %s", out)
	}
	// send --check still follows the actual reply destination.
	send(t, address, "carol", "#wrong", "unrelated channel")
	t.Setenv("AIRC_CHANNEL", "#wrong")
	out = mustCLI(t, address, "send", "--nick", "bob", "--reply-to", second.ID, "--message", "one more", "--check", "--json")
	if !strings.Contains(out, `"reply_to":"`+second.ID+`"`) || !strings.Contains(out, "thanks") || strings.Contains(out, "unrelated channel") {
		t.Fatalf("reply with check: %s", out)
	}
}

func TestEvictedReplyParentsFailWhileRetainedConversationRemainsReadable(t *testing.T) {
	agentEnv(t)
	address := cliTestServerWith(t, server.Config{HistoryLimit: 3})
	root := sentMessage(t, address, "--nick", "alice", "--channel", "room", "--message", "old root")
	first := replyMessage(t, address, "bob", root.ID, "retained answer")
	send(t, address, "alice", "#room", "noise one")
	send(t, address, "alice", "#room", "noise two")
	if _, _, err := cli(t, address, "send", "--nick", "bob", "--reply-to", root.ID, "--message", "lost"); err == nil || !strings.Contains(err.Error(), "not retained") {
		t.Fatalf("expired parent: %v", err)
	}
	if out := mustCLI(t, address, "thread", root.ID, "--json"); strings.Count(out, "\n") != 1 || !strings.Contains(out, first.ID) {
		t.Fatalf("partial conversation: %s", out)
	}
	last := replyMessage(t, address, "alice", first.ID, "continued after eviction")
	if last.ThreadID != root.ID {
		t.Fatalf("root lost after eviction: %+v", last)
	}
	if _, _, err := cli(t, address, "check", "--nick", "alice", "--reply-to", root.ID, "--json"); err == nil || !strings.Contains(err.Error(), "no longer retained") {
		t.Fatalf("evicted direct reply should report retention loss: %v", err)
	}
	if out := mustCLI(t, address, "thread", root.ID, "--json"); !strings.Contains(out, last.ID) {
		t.Fatalf("retained descendant disappeared: %s", out)
	}
}
