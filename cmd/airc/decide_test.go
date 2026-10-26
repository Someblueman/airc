package main

import (
	"strings"
	"testing"
)

func TestDecideThroughTheCLI(t *testing.T) {
	address := chatServer(t, 64)
	offer := cliJSON[decisionRow](t, address, "decide", "--nick", "builder", "--channel", "work", "--from", "sws", "--message", "Ship with the simple parser?", "--option", "yes", "--option", "no, rewrite it")
	if offer.Type != "decision" || offer.State != "open" || offer.Target != "#work" || offer.From != "builder" || offer.Decider != "sws" || offer.Message != "Ship with the simple parser?" || strings.Join(offer.Options, "|") != "yes|no, rewrite it" || offer.Timestamp.IsZero() || offer.AnsweredBy != "" {
		t.Fatalf("offer %+v", offer)
	}

	// The offer is a mention of the decider: a mentions-only check sees it,
	// marked as a decision in text, with its options numbered.
	out := mustCLI(t, address, "check", "--nick", "sws", "--mentions", "--json")
	if messages := checkMessages(t, out); len(messages) != 1 || messages[0].Kind != "decision" || !messages[0].Mentioned || len(messages[0].Choices) != 2 {
		t.Fatalf("sws's mentions %s", out)
	}
	if out := mustCLI(t, address, "history", "#work", "--nick", "reader"); !strings.Contains(out, "[decision "+offer.ID+" for sws] Ship with the simple parser?\n") || !strings.Contains(out, "1. yes\n") || !strings.Contains(out, "2. no, rewrite it") {
		t.Fatalf("history shows the offer as %s", out)
	}
	if out := mustCLI(t, address, "decisions", "--nick", "sws"); !strings.HasPrefix(out, "decision "+offer.ID+" open for sws in #work from builder\n  Ship with the simple parser?\n  1. yes\n  2. no, rewrite it\n") {
		t.Fatalf("sws's decisions read %q", out)
	}

	deniedCLI(t, address, "this decision is for sws", "decide", "answer", offer.ID, "--nick", "reviewer", "--choice", "1")
	deniedCLI(t, address, "option number, a note, or both", "decide", "answer", offer.ID, "--nick", "sws")
	out = mustCLI(t, address, "decide", "answer", offer.ID, "--nick", "sws", "--choice", "2", "--message", "the edge cases matter")
	if !strings.HasPrefix(out, "decision "+offer.ID+" answered by sws: 2. no, rewrite it in #work from builder\n  Ship with") || !strings.HasSuffix(out, "\n  sws: Decided 2 (no, rewrite it): the edge cases matter\n") {
		t.Fatalf("answer printed %q", out)
	}
	answered := jsonLines[decisionRow](t, mustCLI(t, address, "decisions", "--id", offer.ID, "--json"))
	if len(answered) != 1 || answered[0].State != "answered" || answered[0].Choice != 2 || answered[0].AnsweredBy != "sws" || answered[0].Answer != "Decided 2 (no, rewrite it): the edge cases matter" || answered[0].AnsweredAt.IsZero() {
		t.Fatalf("the answered decision %+v", answered)
	}
	// The asker is told, as a mention, in words that name the choice.
	out = mustCLI(t, address, "check", "--nick", "builder", "--mentions")
	if !strings.Contains(out, "[answer to decision "+offer.ID+"] Decided 2 (no, rewrite it): the edge cases matter") {
		t.Fatalf("builder was not told of the answer: %s", out)
	}
	if out := mustCLI(t, address, "decisions", "--nick", "sws"); out != "No decisions.\n" {
		t.Fatalf("sws still has %q", out)
	}
	deniedCLI(t, address, "already answered by sws", "decide", "withdraw", offer.ID, "--nick", "builder")

	// A direct decision goes to its decider alone; a withdrawal tells them.
	direct := cliJSON[decisionRow](t, address, "decide", "--nick", "builder", "--to", "sws", "--message", "Rename the flag?")
	if direct.Target != "sws" || direct.Decider != "sws" || direct.Options != nil {
		t.Fatalf("direct %+v", direct)
	}
	if mine := jsonLines[decisionRow](t, mustCLI(t, address, "decisions", "--nick", "builder", "--json")); len(mine) != 1 || mine[0].ID != direct.ID {
		t.Fatalf("builder's decisions %+v", mine)
	}
	deniedCLI(t, address, "author or an admin", "decide", "withdraw", direct.ID, "--nick", "sws")
	if gone := cliJSON[decisionRow](t, address, "decide", "withdraw", direct.ID, "--nick", "builder", "--message", "answered in the thread"); gone.State != "withdrawn" || gone.AnsweredBy != "" {
		t.Fatalf("withdrawn %+v", gone)
	}
	out = mustCLI(t, address, "check", "--nick", "sws", "--mentions")
	if !strings.Contains(out, "[withdrew decision "+direct.ID+"] answered in the thread") {
		t.Fatalf("sws was not told of the withdrawal: %s", out)
	}
	// A decision without options is answered with a note alone.
	note := cliJSON[decisionRow](t, address, "decide", "--nick", "builder", "--channel", "work", "--from", "sws", "--message", "Which name for the flag?")
	deniedCLI(t, address, "no options", "decide", "answer", note.ID, "--nick", "sws", "--choice", "1")
	if done := cliJSON[decisionRow](t, address, "decide", "answer", note.ID, "--nick", "sws", "--message", "--decision-file"); done.Choice != 0 || done.Answer != "--decision-file" {
		t.Fatalf("note answer %+v", done)
	}
	room := jsonLines[decisionRow](t, mustCLI(t, address, "decisions", "--channel", "work", "--json"))
	if len(room) != 2 || room[0].ID != offer.ID || room[1].ID != note.ID {
		t.Fatalf("the room's decisions %+v", room)
	}
}

func TestDecideCLIRefusesAmbiguousRequests(t *testing.T) {
	address := chatServer(t, 8)
	id := strings.Repeat("ab", 16)
	deniedCLI(t, address, "ask a room with --channel --from, or a nickname with --to", "decide", "--nick", "builder", "--message", "x")
	deniedCLI(t, address, "ask a room with --channel --from, or a nickname with --to", "decide", "--nick", "builder", "--channel", "work", "--message", "x")
	deniedCLI(t, address, "--to names the decider of a direct decision", "decide", "--nick", "builder", "--channel", "work", "--to", "sws", "--message", "x")
	deniedCLI(t, address, "--to names the decider of a direct decision", "decide", "--nick", "builder", "--from", "sws", "--to", "other", "--message", "x")
	deniedCLI(t, address, "--message is required", "decide", "--nick", "builder", "--channel", "work", "--from", "sws")
	deniedCLI(t, address, "--choice applies to answer", "decide", "--nick", "builder", "--to", "sws", "--message", "x", "--choice", "1")
	deniedCLI(t, address, "needs the decision's ID", "decide", "answer", "--nick", "sws")
	deniedCLI(t, address, "a decision step names only the decision's ID", "decide", "answer", id, "--nick", "sws", "--channel", "work")
	deniedCLI(t, address, "a decision step names only the decision's ID", "decide", "withdraw", id, "--nick", "sws", "--option", "a")
	deniedCLI(t, address, "--choice applies to answer", "decide", "withdraw", id, "--nick", "builder", "--choice", "1")
	deniedCLI(t, address, "unknown decision step", "decide", "decline", id, "--nick", "sws")
	deniedCLI(t, address, "choose --channel or --id", "decisions", "--channel", "work", "--id", id)
	deniedCLI(t, address, "decision is not retained", "decide", "answer", id, "--nick", "sws", "--choice", "1")
	deniedCLI(t, address, "ask someone else", "decide", "--nick", "sws", "--to", "sws", "--message", "x")
	deniedCLI(t, address, "at least two", "decide", "--nick", "builder", "--to", "sws", "--message", "x", "--option", "only")
}

// A decision is meant for a person: asking an agent, or a nickname that
// has not said what it is, warns on stderr but is posted all the same.
func TestDecideWarnsWhenTheDeciderIsNotHuman(t *testing.T) {
	address := chatServer(t, 8)
	_, stderr, err := cli(t, address, "decide", "--nick", "builder", "--to", "sws", "--message", "Ship?", "--json")
	if err != nil || !strings.Contains(stderr, "airc: warning: sws is not registered as a human (kind: unset); posting anyway") {
		t.Fatalf("unset kind: %v %q", err, stderr)
	}
	mustCLI(t, address, "profile", "--nick", "sws", "--model", "reasoner")
	if _, stderr, err := cli(t, address, "decide", "--nick", "builder", "--to", "sws", "--message", "Ship?"); err != nil || !strings.Contains(stderr, "(kind: unset)") {
		t.Fatalf("a profile without a kind: %v %q", err, stderr)
	}
}
