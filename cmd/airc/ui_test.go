package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

var uiBase = time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local)

func uiNow() time.Time { return uiBase.Add(10 * time.Minute) }

func uiMessage(id, from, target, body string, at time.Duration) *irc.MessageEvent {
	return &irc.MessageEvent{Type: "message", ID: id, From: from, Target: target, Message: body, Timestamp: uiBase.Add(at)}
}

func newTestModel(channels ...string) *uiModel {
	return newUIModel("me", channels, uiNow)
}

func typeText(m *uiModel, text string) (cmds []uiCmd, quit bool) {
	for _, r := range text {
		m.update(keyIn{kind: keyRune, r: r})
	}
	return m.update(keyIn{kind: keyEnter})
}

func TestParseKeys(t *testing.T) {
	cases := []struct {
		in   string
		want []key
	}{
		{"ab", []key{{keyRune, 'a'}, {keyRune, 'b'}}},
		{"é✓", []key{{keyRune, 'é'}, {keyRune, '✓'}}},
		{"\r", []key{{kind: keyEnter}}},
		{"\x7f", []key{{kind: keyBackspace}}},
		{"\t", []key{{kind: keyTab}}},
		{"\x1b[Z", []key{{kind: keyBackTab}}},
		{"\x1b[A\x1b[B\x1b[C\x1b[D", []key{{kind: keyUp}, {kind: keyDown}, {kind: keyRight}, {kind: keyLeft}}},
		{"\x1bOA", []key{{kind: keyUp}}}, // application cursor mode
		{"\x1b[5~\x1b[6~", []key{{kind: keyPgUp}, {kind: keyPgDn}}},
		{"\x1b[3~\x1b[H\x1b[F\x1b[1~\x1b[4~", []key{{kind: keyDelete}, {kind: keyHome}, {kind: keyEnd}, {kind: keyHome}, {kind: keyEnd}}},
		{"\x03\x04\x0e\x10", []key{{keyCtrl, 'c'}, {keyCtrl, 'd'}, {keyCtrl, 'n'}, {keyCtrl, 'p'}}},
		{"\x1b", []key{{kind: keyEsc}}},
		{"\x1b[99;99X", nil}, // an unrecognised sequence is dropped, not typed
		{"\x00\x1b[200~", nil},
	}
	for _, c := range cases {
		if got := parseKeys([]byte(c.in)); fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("parseKeys(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestBuffersStaySortedWithTheInboxLast(t *testing.T) {
	m := newTestModel("#zulu", "#alpha")
	m.update(msgIn{event: uiMessage("1", "x", "#mike", "hi", 0), history: true})
	var names []string
	for _, b := range m.buffers {
		names = append(names, b.name)
	}
	if fmt.Sprint(names) != "[#alpha #mike #zulu @me]" || m.current != "#alpha" {
		t.Fatalf("buffers = %v current = %s", names, m.current)
	}
}

func TestUnreadAndMentionTracking(t *testing.T) {
	m := newTestModel("#alpha", "#beta")
	m.update(msgIn{event: uiMessage("1", "bob", "#alpha", "visible, no unread", 0)})
	m.update(msgIn{event: uiMessage("2", "bob", "#beta", "plain", time.Second)})
	m.update(msgIn{event: uiMessage("3", "bob", "#beta", "hey @ME look", 2*time.Second)})
	m.update(msgIn{event: uiMessage("4", "me", "#beta", "my own words", 3*time.Second)})
	m.update(msgIn{event: uiMessage("5", "bob", "#beta", "loaded history", -time.Hour), history: true})
	m.update(msgIn{event: uiMessage("6", "bob", "me", "a direct message", 4*time.Second)})
	m.update(msgIn{event: uiMessage("2", "bob", "#beta", "duplicate id", 5*time.Second)})

	alpha, beta, inbox := m.find("#alpha"), m.find("#beta"), m.inbox()
	if alpha.unread != 0 {
		t.Errorf("the open channel must not accumulate unread: %d", alpha.unread)
	}
	if beta.unread != 2 || !beta.mention {
		t.Errorf("#beta unread=%d mention=%v, want 2 and a mention (own, history and duplicates excluded)", beta.unread, beta.mention)
	}
	if inbox.unread != 2 || !inbox.mention || len(inbox.items) != 2 {
		t.Errorf("inbox unread=%d items=%d, want the tag and the direct message", inbox.unread, len(inbox.items))
	}
	m.update(keyIn{kind: keyTab})
	if m.current != "#beta" || beta.unread != 0 || beta.mention {
		t.Errorf("switching should clear unread: current=%s unread=%d", m.current, beta.unread)
	}
}

func TestMessagesKeepTimestampOrderAndAreBounded(t *testing.T) {
	m := newTestModel("#a")
	m.update(msgIn{event: uiMessage("late", "x", "#a", "second", 2*time.Minute)})
	m.update(msgIn{event: uiMessage("early", "x", "#a", "first", time.Minute), history: true})
	b := m.find("#a")
	if first := b.items[0].(*irc.MessageEvent); first.ID != "early" {
		t.Fatalf("older history that arrives late must sort first, got %s", first.ID)
	}
	for i := 0; i < bufferLimit+50; i++ {
		m.update(msgIn{event: uiMessage(fmt.Sprint("n", i), "x", "#a", "filler", time.Hour+time.Duration(i)*time.Second)})
	}
	if len(b.items) != bufferLimit {
		t.Fatalf("buffer holds %d items, want %d", len(b.items), bufferLimit)
	}
}

func TestTypingEditingAndSending(t *testing.T) {
	m := newTestModel("#alpha")
	for _, k := range []key{{keyRune, 'h'}, {keyRune, 'i'}, {keyRune, 'x'}, {kind: keyBackspace}, {kind: keyLeft}, {keyRune, '!'}} {
		m.update(keyIn(k))
	}
	if string(m.input) != "h!i" || m.cursor != 2 {
		t.Fatalf("input = %q cursor = %d", string(m.input), m.cursor)
	}
	m.update(keyIn{kind: keyCtrl, r: 'u'})
	if string(m.input) != "i" || m.cursor != 0 {
		t.Fatalf("ctrl-u left %q at %d", string(m.input), m.cursor)
	}
	m.input, m.cursor = []rune("one two  "), 9
	m.update(keyIn{kind: keyCtrl, r: 'w'})
	if string(m.input) != "one " {
		t.Fatalf("ctrl-w left %q", string(m.input))
	}
	m.update(keyIn{kind: keyEsc})
	if len(m.input) != 0 {
		t.Fatal("escape should clear the input")
	}
	cmds, quit := typeText(m, "  hello room  ")
	if quit || len(cmds) != 1 || cmds[0] != (uiCmd{kind: "send", target: "#alpha", text: "hello room"}) {
		t.Fatalf("send = %#v quit=%v", cmds, quit)
	}
	if cmds, _ := m.update(keyIn{kind: keyEnter}); len(cmds) != 0 {
		t.Fatal("an empty line must send nothing")
	}
}

func TestSlashCommands(t *testing.T) {
	m := newTestModel("#alpha")
	run := func(line string) []uiCmd { cmds, _ := typeText(m, line); return cmds }

	if got := run("/topic Welcome to alpha"); len(got) != 1 || got[0] != (uiCmd{kind: "topic", target: "#alpha", text: "Welcome to alpha"}) {
		t.Fatalf("/topic = %#v", got)
	}
	if got := run("/topic -"); len(got) != 1 || got[0].text != "" || got[0].kind != "topic" {
		t.Fatalf("/topic - should clear, got %#v", got)
	}
	run("/topic")
	if status, _ := m.activeStatus(); !strings.Contains(status, "no topic") {
		t.Fatalf("status = %q", status)
	}
	if got := run("/msg bob  see you there "); len(got) != 1 || got[0] != (uiCmd{kind: "send", target: "bob", text: "see you there"}) {
		t.Fatalf("/msg = %#v", got)
	}
	if got := run("/msg bob"); len(got) != 0 {
		t.Fatalf("/msg without text sent %#v", got)
	}
	got := run("/join newroom")
	if m.current != "#newroom" || m.find("#newroom") == nil || len(got) != 2 || got[1] != (uiCmd{kind: "observe", target: "#newroom"}) {
		t.Fatalf("/join: current=%s cmds=%#v", m.current, got)
	}
	run("/close")
	if m.find("#newroom") != nil || m.current != "#alpha" {
		t.Fatalf("/close: current=%s", m.current)
	}
	run("/nonsense")
	if status, isError := m.activeStatus(); !isError || !strings.Contains(status, "Unknown command") {
		t.Fatalf("status = %q", status)
	}
	m.switchTo("@me")
	if got := run("plain text in the inbox"); len(got) != 0 {
		t.Fatalf("the inbox is read-only, sent %#v", got)
	}
	if status, isError := m.activeStatus(); !isError || !strings.Contains(status, "/msg") {
		t.Fatalf("status = %q", status)
	}
	if _, quit := typeText(m, "/quit"); !quit {
		t.Fatal("/quit should quit")
	}
	if _, quit := m.update(keyIn{kind: keyCtrl, r: 'c'}); !quit {
		t.Fatal("ctrl-c should quit")
	}
	m.input = []rune("draft")
	if _, quit := m.update(keyIn{kind: keyCtrl, r: 'd'}); quit {
		t.Fatal("ctrl-d must not quit while there is text to keep")
	}
}

func TestStatusMessagesExpire(t *testing.T) {
	now := uiBase
	m := newUIModel("me", nil, func() time.Time { return now })
	m.setStatus("hello", false)
	if s, _ := m.activeStatus(); s != "hello" {
		t.Fatal("status should be active")
	}
	now = now.Add(statusHold + time.Second)
	if s, _ := m.activeStatus(); s != "" {
		t.Fatalf("status should have expired, got %q", s)
	}
}

func TestPeriodicRefreshOnlyWhenConnected(t *testing.T) {
	now := uiBase
	m := newUIModel("me", []string{"#a"}, func() time.Time { return now })
	now = now.Add(refreshEvery + time.Second)
	if cmds, _ := m.update(tickIn{}); len(cmds) != 0 {
		t.Fatalf("an offline UI must not poll: %#v", cmds)
	}
	connected := true
	m.update(statusIn{connected: &connected})
	cmds, _ := m.update(tickIn{})
	if fmt.Sprint(cmds) != "[{channels  } {names #a }]" {
		t.Fatalf("refresh = %v", cmds)
	}
	if cmds, _ := m.update(tickIn{}); len(cmds) != 0 {
		t.Fatalf("refreshed twice in a row: %#v", cmds)
	}
}

func TestStartingWithoutAChannelOpensTheFirstOneUnlessTheUserMoved(t *testing.T) {
	m := newTestModel()
	if m.current != "@me" {
		t.Fatalf("with no channels yet the inbox is all there is, got %s", m.current)
	}
	cmds, _ := m.update(channelsIn{[]irc.ChannelInfo{{Name: "#beta"}, {Name: "#alpha"}}})
	if m.current != "#alpha" || len(cmds) != 1 || cmds[0].kind != "names" {
		t.Fatalf("discovery should open the first channel: current=%s cmds=%v", m.current, cmds)
	}
	m = newTestModel()
	m.switchTo("@me") // the user chose the inbox explicitly
	m.update(channelsIn{[]irc.ChannelInfo{{Name: "#alpha"}}})
	if m.current != "@me" {
		t.Fatalf("discovery must not move a user who picked a view, got %s", m.current)
	}
}
