package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

func react(t *testing.T, address, nick, parent, kind string) irc.MessageEvent {
	t.Helper()
	var message irc.MessageEvent
	if err := json.Unmarshal([]byte(mustCLI(t, address, "react", parent, kind, "--nick", nick, "--json")), &message); err != nil {
		t.Fatal(err)
	}
	return message
}

func TestReactionsAreLinkedIdempotentAndDoNotAnswerReplyWaits(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	root := sentMessage(t, address, "--nick", "alice", "--channel", "room", "--message", "What about this approach?")
	seen := react(t, address, "bob", root.ID, "seen")
	if seen.Reaction != "seen" || seen.ReplyTo != root.ID || seen.ThreadID != root.ID {
		t.Fatalf("reaction = %+v", seen)
	}
	if repeat := react(t, address, "BOB", root.ID, "seen"); repeat.ID != seen.ID {
		t.Fatalf("duplicate reaction = %+v", repeat)
	}
	done := make(chan string, 1)
	go func() {
		out, _, err := cli(t, address, "check", "--nick", "alice", "--reply-to", root.ID, "--wait", "3s", "--json")
		if err != nil {
			done <- err.Error()
		} else {
			done <- out
		}
	}()
	checking := react(t, address, "bob", root.ID, "checking")
	select {
	case out := <-done:
		t.Fatalf("reaction answered reply wait: %s", out)
	case <-time.After(100 * time.Millisecond):
	}
	answer := replyMessage(t, address, "bob", root.ID, "The approach misses empty inputs")
	select {
	case out := <-done:
		if ids := historyIDs(t, out); len(ids) != 1 || ids[0] != answer.ID {
			t.Fatalf("reply wait = %s", out)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reply wait did not wake")
	}
	thread := mustCLI(t, address, "thread", root.ID, "--json")
	if len(historyIDs(t, thread)) != 4 || !strings.Contains(thread, `"reaction":"checking"`) {
		t.Fatalf("conversation = %s", thread)
	}
	check := mustCLI(t, address, "check", "--nick", "alice", "--channel", "room", "--json")
	if !strings.Contains(check, checking.ID) || !strings.Contains(check, `"reaction":"seen"`) {
		t.Fatalf("ordinary check lost reactions: %s", check)
	}
	if human := newRenderer(false, 80, true).render(&checking); !strings.Contains(human, "reacted checking to") || !strings.Contains(human, root.ID) {
		t.Fatalf("reaction rendered as text: %s", human)
	}
	dm := sentMessage(t, address, "--nick", "alice", "--to", "bob", "--message", "DM question")
	if response := react(t, address, "bob", dm.ID, "agree"); response.Target != "alice" {
		t.Fatalf("DM reaction route = %+v", response)
	}
	if _, _, err := cli(t, address, "react", dm.ID, "disagree", "--nick", "carol"); err == nil {
		t.Fatal("third participant reacted to DM")
	}
}
