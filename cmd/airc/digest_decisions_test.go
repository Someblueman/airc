package main

import (
	"strings"
	"testing"
)

// The digest lists the open decisions that concern a nick even when nothing
// is unread, says when a human is the one waiting, and leaves decision
// steps out of what waits on you.
func TestDigestListsDecisionsAndWhoIsWaiting(t *testing.T) {
	address := chatServer(t, 64)
	mustCLI(t, address, "profile", "--nick", "sws", "--human")
	asked := cliJSON[decisionRow](t, address, "decide", "--nick", "sws", "--channel", "work", "--from", "builder", "--message", "Ship with the simple parser?", "--option", "yes", "--option", "no")
	own := cliJSON[decisionRow](t, address, "decide", "--nick", "builder", "--to", "sws", "--message", "Rename the flag?")
	agentAsk := cliJSON[decisionRow](t, address, "decide", "--nick", "planner", "--channel", "work", "--from", "builder", "--message", "Which port?")

	d := cliJSON[digestSummary](t, address, "digest", "--nick", "builder", "--channel", "work")
	if len(d.Decisions) != 3 || len(d.Waiting) != 0 || d.DecisionsMore != 0 {
		t.Fatalf("builder's digest %+v", d)
	}
	byID := map[string]digestDecision{}
	for _, item := range d.Decisions {
		byID[item.ID] = item
	}
	if item := byID[asked.ID]; !item.Human || item.From != "sws" || item.Decider != "builder" || strings.Join(item.Options, "/") != "yes/no" || item.Preview != "Ship with the simple parser?" {
		t.Fatalf("the human's decision %+v", item)
	}
	if item := byID[agentAsk.ID]; item.Human || item.Decider != "builder" {
		t.Fatalf("the agent's decision %+v", item)
	}
	if item := byID[own.ID]; item.Human || item.From != "builder" || item.Decider != "sws" || item.Options != nil {
		t.Fatalf("builder's own decision %+v", item)
	}

	text := mustCLI(t, address, "digest", "--nick", "builder", "--channel", "work")
	for _, want := range []string{
		"Decisions waiting on you:\n  [" + asked.ID + "] a human (sws) asks in #work: \"Ship with the simple parser?\" options: yes / no\n  [" + agentAsk.ID + "] planner asks in #work: \"Which port?\"\n",
		"Your open decisions:\n  [" + own.ID + "] you asked sws in sws: \"Rename the flag?\"\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text digest lacks %q:\n%s", want, text)
		}
	}
	// Answered, a decision leaves the digest; its answer waits on nobody.
	mustCLI(t, address, "decide", "answer", asked.ID, "--nick", "builder", "--choice", "1")
	d = cliJSON[digestSummary](t, address, "digest", "--nick", "sws", "--channel", "work")
	if len(d.Decisions) != 2 || d.Decisions[0].ID != own.ID || d.Decisions[1].ID != agentAsk.ID || len(d.Waiting) != 0 {
		t.Fatalf("sws's digest %+v", d.Decisions)
	}
	// A room's open decisions for others are listed to whoever reads the
	// room, not as waiting on them.
	if text := mustCLI(t, address, "digest", "--nick", "reviewer", "--channel", "work"); !strings.Contains(text, "Your open decisions:\n  ["+agentAsk.ID+"] planner asked builder in #work") || strings.Contains(text, "waiting on you:") {
		t.Fatalf("reviewer's digest:\n%s", text)
	}
}
