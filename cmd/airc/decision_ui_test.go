package main

import (
	"strconv"
	"strings"
	"testing"

	"github.com/Someblueman/airc/pkg/irc"
)

func TestUIDecisionCommands(t *testing.T) {
	address := chatServer(t, 64)
	mustCLI(t, address, "profile", "--nick", "sws", "--human")
	h := startUIBackend(t, address, "#room")
	h.until("startup", func() bool { return h.model.connected && h.model.kinds.kind("sws") == "human" })

	// Asked from the UI, with options after |.
	h.command("/decide @builder ship it? | yes | no")
	var id string
	h.until("offer", func() bool {
		s, _ := h.model.activeStatus()
		if fields := strings.Fields(s); len(fields) > 2 && fields[0] == "decision" && strings.HasSuffix(s, "; open for builder") {
			id = fields[2]
		}
		return id != ""
	})
	offer := cliJSON[decisionRow](t, address, "decisions", "--id", id)
	if offer.Decider != "builder" || offer.Message != "ship it?" || strings.Join(offer.Options, "|") != "yes|no" {
		t.Fatalf("the UI asked %+v", offer)
	}
	if _, ok := h.model.decisions[id]; !ok {
		t.Fatal("the UI does not count its own open decision")
	}
	mustCLI(t, address, "decide", "answer", id, "--nick", "builder", "--choice", "1")
	h.until("answer", func() bool { return strings.Contains(h.texts("#room"), "builder: Decided 1 (yes)") })
	if _, open := h.model.decisions[id]; open {
		t.Fatal("the answer did not close the decision in the UI")
	}
	h.command("/decisions")
	h.until("listing", func() bool { s, _ := h.model.activeStatus(); return s == "No decisions" })

	// Asked of the UI's user by a human: the status bar says so, the inbox
	// gets the badge, and /answer with an option number and a note answers.
	asked := cliJSON[decisionRow](t, address, "decide", "--nick", "sws", "--channel", "room", "--from", "me", "--message", "merge now?", "--option", "wait", "--option", "merge")
	h.until("asked", func() bool { n, humans := h.model.pendingDecisions(); return n == 1 && humans == 1 })
	// The inbox already holds builder's answer, a mention of its asker.
	if inbox := h.model.inbox(); inbox.unread != 2 || !inbox.mention || !strings.Contains(h.texts(inbox.name), "sws: merge now?") {
		t.Fatalf("inbox unread=%d mention=%v after a decision for me:\n%s", inbox.unread, inbox.mention, h.texts(inbox.name))
	}
	h.model.status = ""
	if bar := visible(h.model.statusBar(h.model.cur(), 120)); !strings.Contains(bar, "a human is waiting on you: 1 decision waiting on you") {
		t.Fatalf("status bar %q", bar)
	}
	h.command("/answer " + asked.ID + " 2 after the tests")
	h.until("answered", func() bool {
		s, _ := h.model.activeStatus()
		return strings.HasPrefix(s, "decision #room "+asked.ID+" by sws: merge now?") && strings.HasSuffix(s, "; answered by me: 2. merge")
	})
	if n, _ := h.model.pendingDecisions(); n != 0 {
		t.Fatal("the UI still counts the decision it answered")
	}
	if answered := cliJSON[decisionRow](t, address, "decisions", "--id", asked.ID); answered.Choice != 2 || answered.Answer != "Decided 2 (merge): after the tests" {
		t.Fatalf("answered %+v", answered)
	}
	// A decision asked of someone else in the room waits on nobody here; the
	// UI withdraws its own.
	h.command("/decide @sws rename it?")
	var second string
	h.until("second offer", func() bool {
		s, _ := h.model.activeStatus()
		if fields := strings.Fields(s); len(fields) > 2 && fields[0] == "decision" && strings.HasSuffix(s, "; open for sws") {
			second = fields[2]
		}
		return second != ""
	})
	if n, _ := h.model.pendingDecisions(); n != 0 {
		t.Fatal("a decision for someone else counts as waiting on me")
	}
	h.command("/withdraw-decision " + second + " not needed")
	h.until("withdraw", func() bool { s, _ := h.model.activeStatus(); return strings.HasSuffix(s, "; withdrawn") })
	if _, open := h.model.decisions[second]; open {
		t.Fatal("the withdrawal did not close the decision in the UI")
	}
}

// Humans are drawn apart from agents: marked in the member list and sorted
// first among connected sessions, their nicks in one colour, and a registered
// author's brackets bold where a guest's are dim.
func TestMembersAndMessagesMarkHumansAndRegisteredAuthors(t *testing.T) {
	m := busyModel(120, 30)
	m.update(directoryIn{cards: []irc.AgentCard{{Nick: "planner", AgentProfile: irc.AgentProfile{Kind: "human"}}, {Nick: "reviewer", AgentProfile: irc.AgentProfile{Kind: "human"}}}})
	m.update(msgIn{event: &irc.MessageEvent{Type: "message", ID: "6", From: "anvil", Target: "#agents-corner", Message: "registered", Timestamp: uiBase, ChatMetadata: irc.ChatMetadata{AccountID: "acct"}}, history: true})
	var members []string
	for _, row := range m.view()[1:] {
		members = append(members, row[strings.LastIndex(row, "│")+len("│"):])
	}
	text := strings.Join(members, "\n")
	plain := visible(text)
	if strings.Index(plain, "planner") > strings.Index(plain, "anvil") {
		t.Fatalf("the connected human should lead the members:\n%s", plain)
	}
	if !strings.Contains(text, "\x1b[38;5;117m☺") || !strings.Contains(text, "\x1b[2m☺") {
		t.Fatalf("humans should be marked, bright when connected and dim when not:\n%s", text)
	}
	if !strings.Contains(text, "\x1b[1;38;5;117mplanner") || strings.Contains(text, "\x1b[1;38;5;117mbuilder") {
		t.Fatalf("only humans should take the human colour:\n%s", text)
	}
	screen := strings.Join(m.view(), "\n")
	if !strings.Contains(screen, "\x1b[1m<\x1b[0m\x1b[1m\x1b[38;5;"+strconv.Itoa(nickColor("anvil"))+"manvil") {
		t.Fatalf("a registered author's brackets should be bold:\n%s", screen)
	}
	if !strings.Contains(screen, "\x1b[2m<\x1b[0m\x1b[1m\x1b[38;5;"+strconv.Itoa(nickColor("builder"))+"mbuilder") {
		t.Fatalf("a guest's brackets should stay dim:\n%s", screen)
	}
	if !strings.Contains(screen, "\x1b[2m<\x1b[0m\x1b[1m\x1b[38;5;117mplanner") {
		t.Fatalf("a human's nick should take the human colour in messages:\n%s", screen)
	}
	// Without a directory, as in watch, everyone is drawn alike.
	r := newRenderer(true, 80, false)
	line := r.message(uiMessage("7", "planner", "#agents-corner", "hello", 0))
	if strings.Contains(line, "38;5;117m") {
		t.Fatalf("the plain renderer marked a human: %q", line)
	}
}
