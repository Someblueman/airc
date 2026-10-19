package main

import (
	"strings"
	"testing"
)

func TestHandoffThroughTheCLI(t *testing.T) {
	address := chatServer(t, 64)
	offer := cliJSON[handoffRow](t, address, "handoff", "--nick", "planner", "--channel", "work", "--to", "builder", "--message", "finish task 7 on branch work-7")
	if offer.Type != "handoff" || offer.State != "open" || offer.Target != "#work" || offer.For != "builder" || offer.Message != "finish task 7 on branch work-7" || offer.Timestamp.IsZero() {
		t.Fatalf("offer %+v", offer)
	}

	// The offer is a mention of builder: a mentions-only check sees it,
	// marked as a handoff in text.
	out := mustCLI(t, address, "check", "--nick", "builder", "--mentions", "--json")
	if messages := checkMessages(t, out); len(messages) != 1 || messages[0].Kind != "handoff" || !messages[0].Mentioned {
		t.Fatalf("builder's mentions %s", out)
	}
	if out := mustCLI(t, address, "history", "#work", "--nick", "reader"); !strings.Contains(out, "[handoff "+offer.ID+" for builder] finish task 7") {
		t.Fatalf("history shows the offer as %s", out)
	}
	digest := cliJSON[digestSummary](t, address, "digest", "--nick", "builder")
	if len(digest.Handoffs) != 1 || digest.Handoffs[0].ID != offer.ID || len(digest.Waiting) != 0 {
		t.Fatalf("builder's digest %+v", digest)
	}
	if out := mustCLI(t, address, "digest", "--nick", "builder"); !strings.Contains(out, "["+offer.ID+"] from planner for you, open in #work") {
		t.Fatalf("builder's digest reads %s", out)
	}

	deniedCLI(t, address, "this handoff is for builder", "handoff", "accept", offer.ID, "--nick", "reviewer")
	if out := mustCLI(t, address, "handoff", "accept", offer.ID, "--nick", "builder"); !strings.HasPrefix(out, "handoff "+offer.ID+" accepted by builder in #work from planner for builder\n  finish task 7") {
		t.Fatalf("accept printed %q", out)
	}
	mine := jsonLines[handoffRow](t, mustCLI(t, address, "handoffs", "--nick", "planner", "--json"))
	if len(mine) != 1 || mine[0].Holder != "builder" {
		t.Fatalf("planner's handoffs %+v", mine)
	}
	mustCLI(t, address, "handoff", "done", offer.ID, "--nick", "builder", "--message", "merged in #54")

	// Everyone in the room sees it done, the author as a mention.
	out = mustCLI(t, address, "check", "--nick", "planner", "--mentions")
	// A step without a note says what it did, unmarked.
	if !strings.Contains(out, "]: Accepted handoff "+offer.ID+"\n") {
		t.Fatalf("planner was not told of the accept: %s", out)
	}
	if !strings.Contains(out, "[done with handoff "+offer.ID+"] merged in #54") {
		t.Fatalf("planner was not told it is done: %s", out)
	}
	room := jsonLines[handoffRow](t, mustCLI(t, address, "handoffs", "--channel", "work", "--json"))
	if len(room) != 1 || room[0].State != "done" {
		t.Fatalf("the room's handoffs %+v", room)
	}
	if out := mustCLI(t, address, "handoffs", "--nick", "builder"); out != "No handoffs.\n" {
		t.Fatalf("builder still has %q", out)
	}
	deniedCLI(t, address, "already done by builder", "handoff", "withdraw", offer.ID, "--nick", "planner")

	// A room handoff open to anyone is in the digest of each agent in that
	// room, though a one-shot command joins no room.
	open := cliJSON[handoffRow](t, address, "handoff", "--nick", "planner", "--channel", "work", "--message", "triage the flaky test")
	if d := cliJSON[digestSummary](t, address, "digest", "--nick", "reviewer", "--channel", "work"); len(d.Handoffs) != 1 || d.Handoffs[0].ID != open.ID {
		t.Fatalf("reviewer's digest %+v", d.Handoffs)
	}
	if d := cliJSON[digestSummary](t, address, "digest", "--nick", "reviewer", "--channel", "elsewhere"); len(d.Handoffs) != 0 {
		t.Fatalf("a digest of another room lists %+v", d.Handoffs)
	}
	mustCLI(t, address, "handoff", "withdraw", open.ID, "--nick", "planner")

	// A withdrawn handoff names no holder.
	other := cliJSON[handoffRow](t, address, "handoff", "--nick", "planner", "--channel", "work", "--message", "anyone")
	mustCLI(t, address, "handoff", "accept", other.ID, "--nick", "reviewer")
	if gone := cliJSON[handoffRow](t, address, "handoff", "withdraw", other.ID, "--nick", "planner"); gone.State != "withdrawn" || gone.Holder != "" {
		t.Fatalf("withdrawn %+v", gone)
	}
}

func TestHandoffCLIRefusesAmbiguousRequests(t *testing.T) {
	address := chatServer(t, 8)
	deniedCLI(t, address, "hand off to a room", "handoff", "--nick", "planner", "--message", "x")
	deniedCLI(t, address, "--message is required", "handoff", "--nick", "planner", "--channel", "work")
	deniedCLI(t, address, "needs the handoff's ID", "handoff", "done", "--nick", "builder")
	id := strings.Repeat("ab", 16)
	deniedCLI(t, address, "names only the handoff's ID", "handoff", "accept", id, "--nick", "builder", "--channel", "work")
	deniedCLI(t, address, "unknown handoff step", "handoff", "finish", id, "--nick", "builder")
	deniedCLI(t, address, "choose --channel or --id", "handoffs", "--channel", "work", "--id", id)
	deniedCLI(t, address, "handoff is not retained", "handoff", "accept", id, "--nick", "builder")
}
