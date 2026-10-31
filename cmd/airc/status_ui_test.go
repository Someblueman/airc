package main

import (
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

// The member pane marks a nickname with its latest status state, from live
// status posts and from the directory, and the inbox marks a question whose
// thread was resolved.
func TestMemberPaneMarksStatusesAndInboxMarksResolved(t *testing.T) {
	m := newTestModel("#a")
	m.update(namesIn{channel: "#a", nicks: []string{"builder", "planner", "me"}})
	status := uiMessage("s1", "builder", "#a", "Status [working] port the fix", -time.Minute)
	status.Kind, status.Task, status.State = "status", "port the fix", "working"
	m.update(msgIn{event: status})
	pane := visible(strings.Join(m.memberPane(m.find("#a"), 10, 30), "\n"))
	if m.statuses["builder"] != "working" || !strings.Contains(pane, "builder »") || strings.Contains(pane, "planner »") {
		t.Fatalf("after a working status the pane reads\n%s", pane)
	}
	// The directory's word replaces it: done is ✓, and no status clears it.
	m.update(directoryIn{cards: []irc.AgentCard{
		{Nick: "builder", Status: &irc.AgentStatus{Task: "port the fix", State: "done", Target: "#a"}},
		{Nick: "planner", Status: &irc.AgentStatus{Task: "write the notes", State: "blocked", Target: "#a"}},
	}})
	pane = visible(strings.Join(m.memberPane(m.find("#a"), 10, 30), "\n"))
	if !strings.Contains(pane, "builder ✓") || !strings.Contains(pane, "planner !") {
		t.Fatalf("after the directory the pane reads\n%s", pane)
	}
	m.update(directoryIn{cards: []irc.AgentCard{{Nick: "builder"}}})
	if pane = visible(strings.Join(m.memberPane(m.find("#a"), 10, 30), "\n")); strings.Contains(pane, "builder ✓") || !strings.Contains(pane, "planner !") {
		t.Fatalf("after the status cleared the pane reads\n%s", pane)
	}

	// A question for me reaches the inbox; the resolution marks it there and
	// in the room.
	question := uiMessage("q1", "planner", "#a", "me: can you review the parser?", -30*time.Second)
	m.update(msgIn{event: question})
	inbox := m.inbox()
	if len(inbox.items) != 1 {
		t.Fatalf("inbox holds %+v", inbox.items)
	}
	resolution := uiMessage("r1", "planner", "#a", "settled in the DM", -time.Second)
	resolution.Kind, resolution.ReplyTo, resolution.ThreadID = "resolved", "q1", "q1"
	m.update(msgIn{event: resolution})
	if !question.Resolved {
		t.Fatal("the root was not marked resolved")
	}
	for _, b := range []*uiBuffer{inbox, m.find("#a")} {
		if text := visible(strings.Join(m.logLines(b, 100), "\n")); !strings.Contains(text, "[resolved] me: can you review the parser?") {
			t.Fatalf("%s reads\n%s", b.name, text)
		}
	}
	if text := visible(strings.Join(m.logLines(m.find("#a"), 100), "\n")); !strings.Contains(text, "[resolved q1] settled in the DM") {
		t.Fatalf("the room reads\n%s", text)
	}

	// /status posts from the room, lists alone, and refuses a bad state;
	// /resolve takes an ID and a note anywhere.
	m.switchTo("#a")
	m.connected = true
	m.input = []rune("/status port the fix | working | on branch work-7")
	cmds, _ := m.submit()
	if len(cmds) != 1 || cmds[0].kind != "chat" || cmds[0].request.Action != "status" || cmds[0].request.Target != "#a" || cmds[0].request.Task != "port the fix" || cmds[0].request.State != "working" || cmds[0].request.Text != "on branch work-7" {
		t.Fatalf("/status gave %+v", cmds)
	}
	m.input = []rune("/status")
	if cmds, _ = m.submit(); len(cmds) != 1 || cmds[0].request.Action != "statuses" || cmds[0].request.Target != "#a" {
		t.Fatalf("/status alone gave %+v", cmds)
	}
	m.input = []rune("/status port the fix | idle")
	if cmds, _ = m.submit(); len(cmds) != 0 || !m.statusError {
		t.Fatalf("/status with a bad state gave %+v", cmds)
	}
	m.switchTo(inbox.name)
	m.input = []rune("/status port the fix | working")
	if cmds, _ = m.submit(); len(cmds) != 0 || !m.statusError {
		t.Fatalf("/status from the inbox gave %+v", cmds)
	}
	settled := strings.Repeat("c", 32)
	m.input = []rune("/resolve " + settled + " answered in person")
	if cmds, _ = m.submit(); len(cmds) != 1 || cmds[0].request.Action != "resolve" || cmds[0].request.ID != settled || cmds[0].request.Text != "answered in person" {
		t.Fatalf("/resolve gave %+v", cmds)
	}
	m.input = []rune("/resolve")
	if cmds, _ = m.submit(); len(cmds) != 0 || !m.statusError {
		t.Fatalf("/resolve without an ID gave %+v", cmds)
	}
}

// Against a daemon, the UI marks a status posted by the CLI, posts its own,
// lists the room's statuses in the status line, and resolves a question.
func TestUIStatusAndResolveAgainstADaemon(t *testing.T) {
	address := chatServer(t, 64)
	mustCLI(t, address, "status", "--nick", "builder", "--channel", "room", "--task", "port the fix", "--state", "working")
	h := startUIBackend(t, address, "#room")
	h.until("builder's status", func() bool { return h.model.connected && h.model.statuses["builder"] == "working" })
	h.command("/status review the parser | done | shipped")
	h.until("own status", func() bool {
		return h.model.status == "me [done] review the parser in #room" && h.model.statuses["me"] == "done"
	})
	listed := jsonLines[statusRow](t, mustCLI(t, address, "status", "--channel", "room", "--json"))
	if len(listed) != 2 || listed[0].Nick != "me" || listed[0].State != "done" || listed[0].Task != "review the parser" || listed[1].Nick != "builder" {
		t.Fatalf("the room's statuses %+v", listed)
	}
	h.command("/status")
	h.until("listing", func() bool {
		return strings.HasPrefix(h.model.status, "me [done] review the parser in #room; builder [working] port the fix in #room")
	})

	question := send(t, address, "planner", "room", "me: can you review it today?")
	h.until("the question", func() bool { return strings.Contains(h.texts(h.model.inbox().name), "can you review it today?") })
	h.command("/resolve " + question.ID + " answered in person")
	h.until("resolved", func() bool {
		for _, item := range h.model.inbox().items {
			if m, ok := item.(*irc.MessageEvent); ok && m.ID == question.ID && m.Resolved {
				return true
			}
		}
		return false
	})
	if d := cliJSON[digestSummary](t, address, "digest", "--nick", "me"); len(d.Waiting) != 0 {
		t.Fatalf("the digest still waits on %+v", d.Waiting)
	}
}
