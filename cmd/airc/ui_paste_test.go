package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// A message sharing a paste is drawn collapsed: its caption, then one line
// naming the paste and how to open it, in place of the reference line.
func TestUIMessageSharingPasteIsCollapsed(t *testing.T) {
	m := newTestModel("#a")
	id := strings.Repeat("ab", 16)
	info := protocol.PasteInfo{ID: id, Name: "run.log", Language: "text", Size: 12 * 1024, Lines: 300}
	msg := uiMessage("1", "planner", "#a", protocol.PasteMessageBody("the failing run", info), 0)
	msg.Paste = &info
	m.update(msgIn{event: msg})
	b := m.find("#a")
	text := visible(strings.Join(m.logLines(b, 120), "\n"))
	if !strings.Contains(text, "the failing run") || !strings.Contains(text, "[paste run.log, 12 KB, 300 lines] /paste "+id) || strings.Contains(text, "paste:"+id) {
		t.Fatalf("collapsed message reads\n%s", text)
	}
	// Outside the UI the hint is the CLI command.
	r := newRenderer(false, 120, false)
	plain := r.render(msg)
	if !strings.Contains(plain, "[paste run.log, 12 KB, 300 lines] airc paste get "+id) {
		t.Fatalf("watch rendering\n%s", plain)
	}
	bare := uiMessage("2", "planner", "#a", protocol.PasteReferenceLine(info), 1)
	bare.Paste = &info
	if lines := strings.Split(strings.TrimSpace(visible(r.render(bare))), "\n"); len(lines) != 1 || !strings.HasSuffix(lines[0], "airc paste get "+id) {
		t.Fatalf("a message without a caption reads %q", lines)
	}
	cmds, _ := typeText(m, "/paste "+id)
	if len(cmds) != 1 || cmds[0].kind != "paste" || cmds[0].target != id {
		t.Fatalf("/paste produced %+v", cmds)
	}
	cmds, _ = typeText(m, "/paste last")
	if len(cmds) != 1 || cmds[0].target != "1" {
		t.Fatalf("/paste last produced %+v", cmds)
	}
}

// /paste ID opens a read-only view of the paste's lines, 200 a page: /next
// continues, /first restarts, and /close returns to the room.
func TestUIPasteViewPagesLines(t *testing.T) {
	m := newTestModel("#a")
	id := strings.Repeat("cd", 16)
	var content strings.Builder
	for i := 1; i <= 450; i++ {
		fmt.Fprintf(&content, "line %d\n", i)
	}
	entry := irc.ChatEntry{Action: "paste-get", ID: id, From: "planner", Paste: &protocol.PasteInfo{ID: id, Name: "run.log", Language: "text", Size: content.Len(), Lines: 450}}
	m.update(pasteIn{entry: entry, content: []byte(content.String())})
	b := m.cur()
	if !isPasteView(b) || b.name != "paste:"+id || b.returnTo != "#a" {
		t.Fatalf("current view %q", m.current)
	}
	if !strings.Contains(b.topic, "run.log · text · ") || !strings.Contains(b.topic, "by planner · lines 1-200 of 450 · /next continues") {
		t.Fatalf("header %q", b.topic)
	}
	lines := visible(strings.Join(m.logLines(b, 80), "\n"))
	if !strings.HasPrefix(lines, "  1  line 1\n  2  line 2\n") || !strings.Contains(lines, "200  line 200\n") || strings.Contains(lines, "line 201") || !strings.Contains(lines, "250 more lines · /next") {
		t.Fatalf("first page reads\n%s", lines)
	}
	cmds, _ := typeText(m, "/next")
	if len(cmds) != 1 || cmds[0].kind != "query-next" || cmds[0].target != "paste:"+id {
		t.Fatalf("/next produced %+v", cmds)
	}
	m.update(pastePageIn{name: "paste:" + id})
	m.update(pastePageIn{name: "paste:" + id})
	if b.pasteShown != 450 || !strings.Contains(b.topic, "lines 1-450 of 450 · /first restarts") {
		t.Fatalf("after two pages: %d shown, header %q", b.pasteShown, b.topic)
	}
	if lines := visible(strings.Join(m.logLines(b, 80), "\n")); !strings.HasSuffix(lines, "450  line 450") {
		t.Fatalf("last page ends\n%s", lines[len(lines)-100:])
	}
	cmds, _ = typeText(m, "/first")
	if len(cmds) != 1 || cmds[0].kind != "query-first" {
		t.Fatalf("/first produced %+v", cmds)
	}
	m.update(pastePageIn{name: "paste:" + id, first: true})
	if b.pasteShown != 200 {
		t.Fatalf("/first left %d lines shown", b.pasteShown)
	}
	// A long line wraps under its number; the view is read-only.
	m.update(pasteIn{entry: entry, content: []byte(strings.Repeat("x", 100) + "\n")})
	if lines := visible(strings.Join(m.logLines(b, 60), "\n")); !strings.HasPrefix(lines, "1  xxxx") || len(strings.Split(lines, "\n")) < 2 {
		t.Fatalf("long line reads\n%s", lines)
	}
	if cmds, _ := typeText(m, "hello"); len(cmds) != 0 || !m.statusError {
		t.Fatalf("typing into a paste view produced %+v", cmds)
	}
	cmds, _ = typeText(m, "/close")
	if m.current != "#a" || m.find("paste:"+id) != nil || len(cmds) == 0 || cmds[len(cmds)-1].kind != "close-query" {
		t.Fatalf("/close: current %q, cmds %+v", m.current, cmds)
	}
	m.update(pastePageIn{name: "paste:" + id}) // a page for a closed view is dropped
	if m.find("paste:"+id) != nil {
		t.Fatal("paging reopened a closed view")
	}
}
