package main

import (
	"strings"
	"testing"
)

// A resolution closes a question for the digest, marks the thread's root in
// thread and context output, and leaves what arrived in check untouched.
func TestResolveThroughTheCLI(t *testing.T) {
	address := chatServer(t, 64)
	question := send(t, address, "planner", "work", "builder: can you review the parser today?")
	if d := cliJSON[digestSummary](t, address, "digest", "--nick", "builder"); len(d.Waiting) != 1 || d.Waiting[0].ID != question.ID {
		t.Fatalf("builder's digest before resolving %+v", d.Waiting)
	}
	deniedCLI(t, address, "usage: airc resolve MESSAGE_ID", "resolve", "--nick", "builder")
	deniedCLI(t, address, "only the author, who it is addressed to, or an admin may resolve this", "resolve", question.ID, "--nick", "stranger")
	deniedCLI(t, address, "not retained", "resolve", strings.Repeat("ab", 16), "--nick", "builder")

	out := mustCLI(t, address, "resolve", question.ID, "--nick", "builder", "--message", "settled in the DM")
	if out != "resolved "+question.ID+" in #work by builder: settled in the DM\n" {
		t.Fatalf("resolve printed %q", out)
	}
	// A repeat answers the same resolution without posting.
	again := cliJSON[resolveRow](t, address, "resolve", question.ID, "--nick", "builder")
	if again.Type != "resolved" || again.ID != question.ID || again.From != "builder" || again.Message != "settled in the DM" || again.MessageID == "" || again.Timestamp.IsZero() {
		t.Fatalf("the repeat %+v", again)
	}
	thread := jsonLines[checkMessage](t, mustCLI(t, address, "thread", question.ID, "--json"))
	if len(thread) != 2 || thread[0].ID != question.ID || !thread[0].Resolved || thread[1].Kind != "resolved" || thread[1].ID != again.MessageID || thread[1].ReplyTo != question.ID || thread[1].For != "" {
		t.Fatalf("the thread reads %+v", thread)
	}
	if out := mustCLI(t, address, "thread", question.ID); !strings.Contains(out, "[resolved] builder: can you review") || !strings.Contains(out, "[resolved "+question.ID+"] settled in the DM") {
		t.Fatalf("the thread prints %s", out)
	}
	context := cliJSON[conversationContext](t, address, "context", question.ID)
	if len(context.Messages) != 2 || !context.Messages[0].Resolved || context.Messages[1].Resolved {
		t.Fatalf("context %+v", context.Messages)
	}

	// The digest no longer waits on it, and marks the thread resolved; the
	// mention still arrives in check.
	if d := cliJSON[digestSummary](t, address, "digest", "--nick", "builder"); len(d.Waiting) != 0 {
		t.Fatalf("builder's digest after resolving %+v", d.Waiting)
	}
	reviewer := cliJSON[digestSummary](t, address, "digest", "--nick", "reviewer", "--channel", "work")
	if len(reviewer.Rooms) != 1 || len(reviewer.Rooms[0].Threads) != 1 || !reviewer.Rooms[0].Threads[0].Resolved {
		t.Fatalf("reviewer's digest %+v", reviewer.Rooms)
	}
	if out := mustCLI(t, address, "digest", "--nick", "reviewer", "--channel", "work"); !strings.Contains(out, "thread "+question.ID+": 2 messages from planner, builder, starting \"builder: can you review the parser today?\" (resolved)\n") {
		t.Fatalf("reviewer's digest reads %s", out)
	}
	if messages := checkMessages(t, mustCLI(t, address, "check", "--nick", "builder", "--mentions", "--json")); len(messages) != 1 || messages[0].ID != question.ID || !messages[0].Mentioned || !messages[0].Resolved {
		t.Fatalf("builder's mentions %+v", messages)
	}

	// The author resolves their own question; a resolution settled some
	// other way needs no note.
	other := send(t, address, "planner", "work", "reviewer: is the flaky test yours?")
	if d := cliJSON[digestSummary](t, address, "digest", "--nick", "reviewer"); len(d.Waiting) != 1 {
		t.Fatalf("reviewer's digest %+v", d.Waiting)
	}
	if out := mustCLI(t, address, "resolve", other.ID, "--nick", "planner"); out != "resolved "+other.ID+" in #work by planner: Resolved\n" {
		t.Fatalf("the author's resolve printed %q", out)
	}
	if d := cliJSON[digestSummary](t, address, "digest", "--nick", "reviewer"); len(d.Waiting) != 0 {
		t.Fatalf("reviewer's digest after the author resolved %+v", d.Waiting)
	}
}
