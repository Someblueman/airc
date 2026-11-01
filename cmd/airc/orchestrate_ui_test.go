package main

import (
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

// /orchestrate builds the room action from a channel: @nick and a goal, a
// goal alone, or off; the answer and a live signal set the top bar and the
// status line, and redraw an open board.
func TestUIOrchestrateCommandAndTopBar(t *testing.T) {
	m := newTestModel("#a")
	m.switchTo("#a")
	m.connected = true
	submit := func(text string) []uiCmd {
		t.Helper()
		m.input = []rune(text)
		cmds, _ := m.submit()
		return cmds
	}
	cmds := submit("/orchestrate @lead ship the parser fix")
	if len(cmds) != 1 || cmds[0].kind != "orchestrate" || cmds[0].target != "#a" {
		t.Fatalf("/orchestrate gave %+v", cmds)
	}
	if r := cmds[0].request; r.Action != "room" || r.Target != "#a" || r.Seconds != -1 || r.Limit != -1 || r.Orchestrator == nil || *r.Orchestrator != "lead" || r.Goal == nil || *r.Goal != "ship the parser fix" {
		t.Fatalf("/orchestrate request %+v", r)
	}
	if cmds = submit("/orchestrate ship the lexer too"); len(cmds) != 1 || cmds[0].request.Orchestrator != nil || *cmds[0].request.Goal != "ship the lexer too" {
		t.Fatalf("/orchestrate goal gave %+v", cmds)
	}
	if cmds = submit("/orchestrate off"); len(cmds) != 1 || cmds[0].request.Orchestrator == nil || *cmds[0].request.Orchestrator != "" || cmds[0].request.Goal != nil {
		t.Fatalf("/orchestrate off gave %+v", cmds)
	}
	if cmds = submit("/orchestrate @bad!nick goal"); len(cmds) != 0 || !m.statusError {
		t.Fatalf("a bad nickname gave %+v", cmds)
	}
	m.switchTo(m.inbox().name)
	if cmds = submit("/orchestrate @lead goal"); len(cmds) != 0 || !m.statusError {
		t.Fatalf("/orchestrate outside a channel gave %+v", cmds)
	}

	// The answer to this UI's own request, and a live signal from someone
	// else, set the top bar; a signal names who changed it.
	m.switchTo("#a")
	m.update(orchestrateIn{channel: "#a", orchestrator: "lead", goal: "ship the parser fix"})
	if header := visible(m.header(m.cur(), 120)); !strings.Contains(header, "#a  │  ⟐ lead: ship the parser fix") {
		t.Fatalf("top bar %q", header)
	}
	if s, _ := m.activeStatus(); s != "#a orchestrated by lead: ship the parser fix" {
		t.Fatalf("status %q", s)
	}
	m.update(boardIn{room: "#a"})
	m.switchTo("#a")
	cmds, _ = m.update(signalIn{entry: irc.ChatEntry{Action: "orchestrate", Target: "#a", From: "boss", ExpiresAt: uiNow().Add(time.Minute)}})
	if s, _ := m.activeStatus(); s != "boss: #a is no longer orchestrated" {
		t.Fatalf("status after off %q", s)
	}
	if strings.Contains(visible(m.header(m.cur(), 120)), "⟐") {
		t.Fatal("the top bar kept the orchestrator after off")
	}
	if len(cmds) != 1 || cmds[0].kind != "board" || cmds[0].target != "#a" || cmds[0].text != "refresh" {
		t.Fatalf("the signal did not redraw the open board: %+v", cmds)
	}

	// Reading the room on showing it sets the top bar too.
	m.update(charterIn{channel: "#a", orchestrator: "lead", goal: "again"})
	if header := visible(m.header(m.cur(), 120)); !strings.Contains(header, "⟐ lead: again") {
		t.Fatalf("top bar after a read %q", header)
	}
}

// In an orchestrated room the board leads with the goal and ends with where
// everyone is.
func TestBoardShowsTheGoalAndWhereEveryoneIs(t *testing.T) {
	m := newTestModel("#a")
	open := irc.ChatEntry{Action: "handoff", Target: "#a", ID: "aaaaaaaa1111", From: "lead", State: "open", Message: &irc.MessageMetadata{ID: "aaaaaaaa1111", From: "lead", Target: "#a", Message: "port the lexer", Timestamp: uiBase.Add(-2 * time.Minute)}}
	statuses := []irc.ChatEntry{
		{Action: "status", Target: "#a", From: "builder", State: "working", Text: "port the lexer", At: uiBase.Add(-time.Minute)},
		{Action: "status", Target: "#a", From: "reviewer", State: "done", Text: "review the parser", Link: "https://github.com/o/r/pull/54", At: uiBase.Add(-time.Hour)},
	}
	m.update(boardIn{room: "#a", entries: []irc.ChatEntry{open}, orchestrator: "lead", goal: "ship the parser fix", statuses: statuses})
	text := helpText(m.cur().help)
	want := "Goal  ship the parser fix  (lead orchestrates)\n" +
		"Open (1)\naaaaaaaa  anyone  12m  port the lexer\nIn progress (0)\nBlocked (0)\nDone (last week) (0)\n" +
		"Where everyone is (2)\nbuilder  [working] port the lexer  11m\nreviewer  [done] review the parser  1h  https://github.com/o/r/pull/54"
	if text != want {
		t.Fatalf("board reads\n%s\nwant\n%s", text, want)
	}
}

// Against a daemon, /orchestrate authenticates with the default admin
// credential, the room's top bar follows, and /board leads with the goal.
func TestUIOrchestrateAgainstTheDaemon(t *testing.T) {
	address, token := chatServerWithToken(t, 64)
	t.Setenv("AIRC_ADMIN_TOKEN_FILE", token)
	mustCLI(t, address, "user", "create", "--nick", "lead", "--agent")
	send(t, address, "writer", "#room", "hello")
	h := startUIBackend(t, address, "#room")
	h.until("startup", func() bool { return h.model.connected && strings.Contains(h.texts("#room"), "hello") })
	h.command("/orchestrate @lead ship the parser fix")
	h.until("the top bar", func() bool {
		return strings.Contains(visible(h.model.header(h.model.find("#room"), 120)), "⟐ lead: ship the parser fix")
	})
	h.until("the announcement", func() bool { return strings.Contains(h.texts("#room"), "@agents lead orchestrates #room") })
	h.command("/board")
	h.until("the board's goal", func() bool {
		b := h.model.find("board:#room")
		return b != nil && strings.HasPrefix(helpText(b.help), "Goal  ship the parser fix  (lead orchestrates)\n")
	})
}
