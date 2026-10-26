package main

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

// The board lays a room's handoffs out by state, with who holds each, how
// old it is, why it is blocked and where its result is; a redraw of a closed
// board is dropped, and /close returns to the room.
func TestBoardLinesGroupHandoffsByState(t *testing.T) {
	m := newTestModel("#a")
	offer := func(id, from, state, holder, text string, at time.Duration) irc.ChatEntry {
		return irc.ChatEntry{Action: "handoff", Target: "#a", ID: id, From: from, State: state, Holder: holder, Message: &irc.MessageMetadata{ID: id, From: from, Target: "#a", Message: text, Timestamp: uiBase.Add(at)}}
	}
	blocked := offer("cccccccc3333", "planner", "blocked", "builder", "port the fix", -time.Hour)
	blocked.BlockedReason, blocked.BlockedAt = "waiting on CI\nsecond line", uiBase.Add(-5*time.Minute)
	done := offer("dddddddd4444", "planner", "done", "builder", "write the notes", -3*time.Hour)
	done.Link = "https://github.com/o/r/pull/54"
	addressed := offer("eeeeeeee5555", "planner", "open", "", "review the parser", -time.Minute)
	addressed.Message.For = "reviewer"
	entries := []irc.ChatEntry{
		offer("aaaaaaaa1111", "planner", "open", "", "triage the flaky test\nmore detail", -2*time.Minute),
		offer("bbbbbbbb2222", "planner", "accepted", "builder", "finish task 7", -30*time.Minute),
		blocked, done, addressed,
		offer("ffffffff6666", "planner", "withdrawn", "", "never mind", -time.Minute),
	}
	m.update(boardIn{room: "#a", entries: entries})
	b := m.cur()
	if !isBoard(b) || b.name != "board:#a" || b.returnTo != "#a" {
		t.Fatalf("current view %q", m.current)
	}
	text := helpText(b.help)
	// The test model's clock is ten minutes past uiBase.
	want := "Open (2)\naaaaaaaa  anyone  12m  triage the flaky test\neeeeeeee  for reviewer  11m  review the parser\n" +
		"In progress (1)\nbbbbbbbb  builder  40m  finish task 7\n" +
		"Blocked (1)\ncccccccc  builder  1h  port the fix  blocked 15m: waiting on CI\n" +
		"Done (last week) (1)\ndddddddd  builder  3h  write the notes  https://github.com/o/r/pull/54"
	if text != want {
		t.Fatalf("board reads\n%s\nwant\n%s", text, want)
	}
	if !strings.HasPrefix(b.topic, "2 open · 1 in progress · 1 blocked · 1 done this week") {
		t.Fatalf("header %q", b.topic)
	}
	if rendered := strings.Join(m.logLines(b, 100), "\n"); !strings.Contains(visible(rendered), "Blocked (1)") || !strings.Contains(visible(rendered), "waiting on CI") {
		t.Fatalf("rendered board\n%s", rendered)
	}

	// A redraw keeps the view where it is; a closed board is not reopened.
	m.switchTo("#a")
	m.update(boardIn{room: "#a", entries: entries[:1], refresh: true})
	if m.current != "#a" || !strings.HasPrefix(m.find("board:#a").topic, "1 open · 0 in progress") {
		t.Fatalf("refresh switched to %q or did not redraw: %q", m.current, m.find("board:#a").topic)
	}
	m.switchTo("board:#a")
	m.input = []rune("/close")
	cmds, _ := m.submit()
	closed := slices.ContainsFunc(cmds, func(c uiCmd) bool { return c.kind == "close-query" && c.target == "board:#a" })
	if m.current != "#a" || m.find("board:#a") != nil || !closed {
		t.Fatalf("close left %q, cmds %+v", m.current, cmds)
	}
	m.update(boardIn{room: "#a", entries: entries, refresh: true})
	if m.find("board:#a") != nil {
		t.Fatal("a refresh reopened a closed board")
	}
	// Without a room, /board needs one.
	m.switchTo(m.inbox().name)
	m.input = []rune("/board")
	if cmds, _ := m.submit(); len(cmds) != 0 || !m.statusError {
		t.Fatalf("/board from the inbox gave %+v", cmds)
	}
	m.input = []rune("/board a")
	if cmds, _ := m.submit(); len(cmds) != 1 || cmds[0].kind != "board" || cmds[0].target != "#a" {
		t.Fatalf("/board a gave %+v", cmds)
	}
}

// The UI's board reads the room's handoffs and redraws itself when a live
// step lands in the room.
func TestUIBoardShowsARoomsHandoffsAndFollowsLiveSteps(t *testing.T) {
	address := chatServer(t, 64)
	open := cliJSON[handoffRow](t, address, "handoff", "--nick", "planner", "--channel", "room", "--message", "triage the flaky test")
	held := cliJSON[handoffRow](t, address, "handoff", "--nick", "planner", "--channel", "room", "--message", "finish task 7")
	mustCLI(t, address, "handoff", "accept", held.ID, "--nick", "builder")
	stuck := cliJSON[handoffRow](t, address, "handoff", "--nick", "planner", "--channel", "room", "--message", "port the fix")
	mustCLI(t, address, "handoff", "accept", stuck.ID, "--nick", "builder")
	mustCLI(t, address, "handoff", "blocked", stuck.ID, "--nick", "builder", "--message", "waiting on CI")
	finished := cliJSON[handoffRow](t, address, "handoff", "--nick", "planner", "--channel", "room", "--message", "write the notes")
	mustCLI(t, address, "handoff", "accept", finished.ID, "--nick", "builder")
	mustCLI(t, address, "handoff", "done", finished.ID, "--nick", "builder", "--link", "https://github.com/o/r/pull/54")

	h := startUIBackend(t, address, "#room")
	h.until("startup", func() bool { return h.model.connected })
	h.command("/board")
	board := func() string {
		if b := h.model.find("board:#room"); b != nil {
			return helpText(b.help)
		}
		return ""
	}
	h.until("board", func() bool {
		return h.model.current == "board:#room" && strings.Contains(board(), "Done (last week) (1)")
	})
	text := board()
	for _, want := range []string{
		"Open (1)\n" + open.ID[:8] + "  anyone  ",
		"In progress (1)\n" + held.ID[:8] + "  builder  ",
		"Blocked (1)\n" + stuck.ID[:8] + "  builder  ",
		"port the fix  blocked now: waiting on CI",
		"Done (last week) (1)\n" + finished.ID[:8] + "  builder  ",
		"write the notes  https://github.com/o/r/pull/54",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("board lacks %q:\n%s", want, text)
		}
	}
	if topic := h.model.cur().topic; !strings.HasPrefix(topic, "1 open · 1 in progress · 1 blocked · 1 done this week") {
		t.Fatalf("header %q", topic)
	}

	// A live step redraws the board where the user is.
	mustCLI(t, address, "handoff", "accept", open.ID, "--nick", "reviewer")
	h.until("live accept", func() bool { return strings.Contains(board(), "In progress (2)") })
	if h.model.current != "board:#room" || !strings.Contains(board(), "Open (0)") || !strings.Contains(board(), open.ID[:8]+"  reviewer  ") {
		t.Fatalf("after a live accept the board reads\n%s", board())
	}
	mustCLI(t, address, "handoff", "accept", stuck.ID, "--nick", "builder")
	h.until("live resume", func() bool {
		return strings.Contains(board(), "Blocked (0)") && strings.Contains(board(), "In progress (3)")
	})
	h.command("/close")
	if h.model.current != "#room" || h.model.find("board:#room") != nil {
		t.Fatalf("close left %q", h.model.current)
	}
}
