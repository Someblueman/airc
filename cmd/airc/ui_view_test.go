package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Someblueman/airc/pkg/irc"
)

func busyModel(width, height int) *uiModel {
	m := newTestModel("#agents-corner", "#side-project")
	m.update(resizeIn{width, height})
	connected := true
	m.update(statusIn{connected: &connected})
	m.update(topicIn{channel: "#agents-corner", topic: "Welcome! Post status here; tag @planner for decisions."})
	m.update(msgIn{event: uiMessage("1", "planner", "#agents-corner", "builder: please implement task 7", 0), history: true})
	m.update(msgIn{event: uiMessage("2", "builder", "#agents-corner", "on it", time.Minute), history: true})
	m.update(msgIn{event: uiMessage("3", "reviewer", "#agents-corner", "multi\nline\nmessage", 2*time.Minute), history: true})
	m.update(namesIn{channel: "#agents-corner", nicks: []string{"anvil", "planner"}})
	m.update(msgIn{event: uiMessage("4", "kestrel", "#side-project", "hello @me", 3*time.Minute)})
	m.update(msgIn{event: uiMessage("5", "kestrel", "#side-project", "again", 4*time.Minute)})
	return m
}

func TestEveryRowFillsTheTerminalExactly(t *testing.T) {
	for _, size := range [][2]int{{140, 40}, {100, 30}, {90, 24}, {80, 24}, {70, 20}, {60, 12}, {50, 10}, {30, 6}} {
		m := busyModel(size[0], size[1])
		rows := m.view()
		if len(rows) != size[1] {
			t.Errorf("%dx%d: %d rows", size[0], size[1], len(rows))
		}
		for i, row := range rows {
			if width := visibleWidth(row); width != size[0] {
				t.Errorf("%dx%d row %d is %d columns wide: %q", size[0], size[1], i, width, ansiPattern.ReplaceAllString(row, ""))
			}
			if strings.ContainsAny(row, "\n\r") {
				t.Errorf("%dx%d row %d contains a line break", size[0], size[1], i)
			}
		}
	}
}

func TestPanesAppearAsTheTerminalWidens(t *testing.T) {
	plain := func(m *uiModel) string { return ansiPattern.ReplaceAllString(strings.Join(m.view(), "\n"), "") }
	wide, medium, narrow, tiny := plain(busyModel(120, 30)), plain(busyModel(90, 30)), plain(busyModel(70, 30)), plain(busyModel(50, 30))
	for name, text := range map[string]string{"wide": wide, "medium": medium} {
		if !strings.Contains(text, "Channels") || !strings.Contains(text, "Members") {
			t.Errorf("%s layout lacks a pane:\n%s", name, text)
		}
	}
	if !strings.Contains(narrow, "Channels") || strings.Contains(narrow, "Members") {
		t.Errorf("a 70-column terminal should keep the channel list and drop the members:\n%s", narrow)
	}
	body := strings.Join(strings.Split(tiny, "\n")[1:], "\n") // the header bar has its own divider
	if strings.Contains(tiny, "Channels") || strings.Contains(tiny, "Members") || strings.Contains(body, "│") {
		t.Errorf("a 50-column terminal should show only the conversation:\n%s", tiny)
	}
}

func TestHeaderShowsChannelTopicAndConnection(t *testing.T) {
	m := busyModel(120, 30)
	header := ansiPattern.ReplaceAllString(m.view()[0], "")
	for _, want := range []string{"#agents-corner", "Welcome! Post status here; tag @planner for decisions.", "● online"} {
		if !strings.Contains(header, want) {
			t.Errorf("header %q lacks %q", header, want)
		}
	}
	if !strings.HasPrefix(m.view()[0], "\x1b[7;1m") {
		t.Error("the header should be a reverse-video bar")
	}
	m.update(topicIn{channel: "#agents-corner", topic: strings.Repeat("long ", 60)})
	if long := m.view()[0]; visibleWidth(long) != 120 || !strings.Contains(long, "…") {
		t.Errorf("a long topic should be truncated with an ellipsis: %q", ansiPattern.ReplaceAllString(long, ""))
	}
	offline := false
	m.update(statusIn{connected: &offline})
	if h := ansiPattern.ReplaceAllString(m.view()[0], ""); !strings.Contains(h, "○ offline") {
		t.Errorf("offline state missing: %q", h)
	}
	m.switchTo("@me")
	if h := ansiPattern.ReplaceAllString(m.view()[0], ""); !strings.Contains(h, "@me") || !strings.Contains(h, "direct messages") {
		t.Errorf("inbox header = %q", h)
	}
}

func TestChannelListHighlightsSelectionAndUnread(t *testing.T) {
	m := busyModel(120, 30)
	var channelRows []string
	for _, row := range m.view()[1:] {
		left := strings.SplitN(row, "│", 2)[0]
		if strings.Contains(left, "#") || strings.Contains(left, "@me") {
			channelRows = append(channelRows, left)
		}
	}
	if len(channelRows) != 3 {
		t.Fatalf("expected two channels and the inbox, got %d rows: %q", len(channelRows), channelRows)
	}
	if !strings.Contains(channelRows[0], "\x1b[7m") || !strings.Contains(channelRows[0], "#agents-corner") {
		t.Errorf("the open channel should be reverse video: %q", channelRows[0])
	}
	side := ansiPattern.ReplaceAllString(channelRows[1], "")
	if !strings.Contains(side, "#side-project") || !strings.HasSuffix(strings.TrimRight(side, " "), "2!") {
		t.Errorf("unread badge with mention marker missing: %q", side)
	}
	if !strings.Contains(channelRows[1], "\x1b[1;38;5;203m") {
		t.Errorf("a channel with a mention should be highlighted: %q", channelRows[1])
	}
	if inbox := ansiPattern.ReplaceAllString(channelRows[2], ""); !strings.HasSuffix(strings.TrimRight(inbox, " "), "1!") {
		t.Errorf("the inbox should count the tag: %q", inbox)
	}
}

func TestMembersShowConnectedSessionsAndRecentSpeakers(t *testing.T) {
	m := busyModel(120, 30)
	var members []string
	for _, row := range m.view()[1:] {
		members = append(members, ansiPattern.ReplaceAllString(row[strings.LastIndex(row, "│")+len("│"):], ""))
	}
	text := strings.Join(members, "\n")
	if !strings.Contains(members[0], "Members (4)") { // anvil + planner connected, builder + reviewer spoke
		t.Errorf("header = %q\n%s", members[0], text)
	}
	order := []string{"anvil", "planner", "reviewer", "builder"} // connected first, then newest speaker
	last := -1
	for _, nick := range order {
		at := strings.Index(text, nick)
		if at < 0 || at < last {
			t.Fatalf("members out of order or missing %q:\n%s", nick, text)
		}
		last = at
	}
	if !strings.Contains(text, "8m") || !strings.Contains(text, "9m") {
		t.Errorf("recent speakers should show how long ago they spoke:\n%s", text)
	}
	if !strings.Contains(strings.Join(m.view(), "\n"), "\x1b[38;5;41m●") {
		t.Error("connected sessions should carry a green marker")
	}
}

func TestMessagesRenderInTheCenterPane(t *testing.T) {
	m := busyModel(120, 30)
	text := ansiPattern.ReplaceAllString(strings.Join(m.view(), "\n"), "")
	for _, want := range []string{"<planner>", "please implement task 7", "<builder> on it", "multi", "line", "message"} {
		if !strings.Contains(text, want) {
			t.Errorf("conversation lacks %q:\n%s", want, text)
		}
	}
}

func TestScrollingClampsAndReportsPosition(t *testing.T) {
	m := newTestModel("#a")
	m.update(resizeIn{100, 12})
	for i := 0; i < 40; i++ {
		m.update(msgIn{event: uiMessage(fmt.Sprint(i), "bob", "#a", fmt.Sprint("line number ", i), time.Duration(i)*time.Second), history: true})
	}
	bottom := ansiPattern.ReplaceAllString(strings.Join(m.view(), "\n"), "")
	if !strings.Contains(bottom, "line number 39") || strings.Contains(bottom, "line number 0\n") {
		t.Fatalf("a fresh view should show the newest lines:\n%s", bottom)
	}
	m.update(keyIn{kind: keyPgUp})
	scrolled := m.view()
	if status := ansiPattern.ReplaceAllString(scrolled[len(scrolled)-2], ""); !strings.Contains(status, "scrolled back") {
		t.Errorf("scrolling should say so: %q", status)
	}
	for i := 0; i < 50; i++ {
		m.update(keyIn{kind: keyPgUp})
	}
	top := ansiPattern.ReplaceAllString(strings.Join(m.view(), "\n"), "")
	if !strings.Contains(top, "line number 0") || m.cur().scroll > 60 {
		t.Fatalf("scrolling should clamp at the oldest line (scroll=%d):\n%s", m.cur().scroll, top)
	}
	for i := 0; i < 80; i++ {
		m.update(keyIn{kind: keyPgDn})
	}
	if m.cur().scroll != 0 {
		t.Fatalf("scroll should return to the bottom, got %d", m.cur().scroll)
	}
	// New messages do not yank the viewport while someone is reading older ones.
	m.update(keyIn{kind: keyPgUp})
	before := m.cur().scroll
	m.update(msgIn{event: uiMessage("new", "bob", "#a", "arrived while reading", time.Hour)})
	if m.cur().scroll <= before {
		t.Errorf("viewport should stay on the same lines: scroll %d then %d", before, m.cur().scroll)
	}
}

func TestInputRowScrollsToKeepTheCursorVisible(t *testing.T) {
	m := newTestModel("#a")
	m.update(resizeIn{40, 10})
	var words []string
	for i := 0; i < 20; i++ {
		words = append(words, fmt.Sprintf("w%02d", i))
	}
	m.input = []rune(strings.Join(words, " "))
	m.cursor = len(m.input)
	row, col := m.inputRow(40)
	if visibleWidth(row) != 40 || col < 1 || col > 40 {
		t.Fatalf("input row width %d, cursor column %d", visibleWidth(row), col)
	}
	if plain := ansiPattern.ReplaceAllString(row, ""); !strings.Contains(plain, "w19") || strings.Contains(plain, "w00") {
		t.Fatalf("with the cursor at the end, the end of the line should be visible: %q", plain)
	}
	m.cursor = 0
	row, col = m.inputRow(40)
	if plain := ansiPattern.ReplaceAllString(row, ""); col != utf8.RuneCountInString("[me] ")+1 || !strings.Contains(plain, "w00") || strings.Contains(plain, "w19") {
		t.Fatalf("with the cursor at the start, the start should be visible: col=%d row=%q", col, plain)
	}
	if r, c := m.cursorPosition(); r != m.height || c < 1 {
		t.Fatalf("cursor position = %d,%d", r, c)
	}
}

func TestLiveTopicChangesAppearInTheConversation(t *testing.T) {
	m := busyModel(160, 30) // wide enough that the notice stays on one line
	m.update(topicIn{channel: "#agents-corner", topic: "New rules", by: "planner"})
	text := ansiPattern.ReplaceAllString(strings.Join(m.view(), "\n"), "")
	if !strings.Contains(text, "planner set the topic of #agents-corner: New rules") {
		t.Errorf("topic change not shown:\n%s", text)
	}
	if !strings.Contains(ansiPattern.ReplaceAllString(m.view()[0], ""), "New rules") {
		t.Error("the header should update")
	}
	count := len(m.cur().items)
	m.update(topicIn{channel: "#agents-corner", topic: "New rules", by: "planner"})
	if len(m.cur().items) != count {
		t.Error("an unchanged topic must not be announced again")
	}
}

func TestChannelDirectoryAddsChannelsWithTheirHeaders(t *testing.T) {
	m := newTestModel()
	m.update(channelsIn{[]irc.ChannelInfo{{Name: "#ops", Topic: "On call: dana"}, {Name: "#dev", Messages: 3}}})
	if len(m.buffers) != 3 || m.buffers[0].name != "#dev" || m.find("#ops").topic != "On call: dana" {
		t.Fatalf("directory not applied: %v", m.buffers)
	}
}

// Wrapped text must fit its pane exactly. A renderer that wraps wider than the
// pane would have its lines clipped mid-word.
func TestLogLinesNeverExceedThePaneWidth(t *testing.T) {
	m := busyModel(120, 30)
	m.update(msgIn{event: uiMessage("long", "musespark", "#agents-corner",
		"sws: please review https://example.com/a/very/long/path/that/cannot/be/broken/anywhere and "+strings.Repeat("words ", 30)+"\n- a bullet with "+strings.Repeat("more ", 20), 5*time.Minute)})
	for _, width := range []int{24, 30, 36, 43, 50, 72, 100} {
		for _, name := range []string{"#agents-corner", "@me"} {
			b := m.find(name)
			b.version++ // drop any cached rendering
			for _, line := range m.logLines(b, width) {
				if got := visibleWidth(line); got > width {
					t.Errorf("%s at width %d: a line is %d columns wide: %q", name, width, got, ansiPattern.ReplaceAllString(line, ""))
				}
			}
		}
	}
	// And nothing is lost: every word survives a narrow pane.
	b := m.find("#agents-corner")
	b.version++
	joined := ansiPattern.ReplaceAllString(strings.Join(m.logLines(b, 30), " "), "")
	if strings.Count(joined, "words") != 30 {
		t.Errorf("wrapping dropped text: %d of 30 'words' survived", strings.Count(joined, "words"))
	}
}
