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

// typeText starts a fresh online command; explicit draft/delivery tests use keys.
func typeText(m *uiModel, text string) (cmds []uiCmd, quit bool) {
	m.connected = true
	if m.pending != nil {
		m.update(deliveryIn{cmd: *m.pending})
	}
	m.input, m.cursor = nil, 0
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
		{"\x1b\r", []key{{keyNewline, '\n'}}},                                                             // Alt-Enter as Meta
		{"\x1b[27;2;13~\x1b[27;3;13~", []key{{keyNewline, '\n'}, {keyNewline, '\n'}}},                     // xterm modifyOtherKeys Shift- and Alt-Enter
		{"\x1b[13;2u\x1b[13;3u\x1b[13u", []key{{keyNewline, '\n'}, {keyNewline, '\n'}, {kind: keyEnter}}}, // kitty
		{"\x1b[99;99X", nil}, // an unrecognised sequence is dropped, not typed
		{"\x00\x1b[200~", nil},
	}
	for _, c := range cases {
		decoder := keyDecoder{}
		got := decoder.feed([]byte(c.in))
		got = append(got, decoder.idle()...)
		if fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("parseKeys(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestBuffersStaySortedBeforeTheInboxAndDMAudit(t *testing.T) {
	m := newTestModel("#zulu", "#alpha")
	m.update(msgIn{event: uiMessage("1", "x", "#mike", "hi", 0), history: true})
	var names []string
	for _, b := range m.buffers {
		names = append(names, b.name)
	}
	if fmt.Sprint(names) != "[#alpha #mike #zulu @me @*]" || m.current != "#alpha" {
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
	for i := range bufferLimit + 50 {
		m.update(msgIn{event: uiMessage(fmt.Sprint("n", i), "x", "#a", "filler", time.Hour+time.Duration(i)*time.Second)})
	}
	if len(b.items) != bufferLimit {
		t.Fatalf("buffer holds %d items, want %d", len(b.items), bufferLimit)
	}
	if len(b.seen) > bufferLimit {
		t.Fatalf("deduplication metadata grew beyond the visible buffer: %d", len(b.seen))
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
	m.connected = true
	m.update(keyIn{kind: keyRune, r: 'a'})
	if cmds, _ := m.update(keyIn{keyNewline, '\n'}); len(cmds) != 0 || string(m.input) != "a\n" || m.cursor != 2 {
		t.Fatalf("Alt-Enter sent %v and left %q at %d", cmds, string(m.input), m.cursor)
	}
	m.update(keyIn{kind: keyRune, r: 'b'})
	if cmds, _ := m.update(keyIn{kind: keyEnter}); len(cmds) != 1 || cmds[0].text != "a\nb" {
		t.Fatalf("a two-line draft sent %+v", cmds)
	}
	m.update(deliveryIn{cmd: *m.pending})
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
	// Observed, then its charter read once observed.
	if m.current != "#newroom" || m.find("#newroom") == nil || len(got) != 3 || got[1] != (uiCmd{kind: "observe", target: "#newroom"}) || got[2] != (uiCmd{kind: "charter", target: "#newroom"}) {
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
	if len(cmds) != 2 || cmds[0].kind != "channels" || cmds[1].kind != "names" || cmds[1].target != "#a" {
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

func typeKeys(m *uiModel, text string) {
	for _, r := range text {
		m.update(keyIn{kind: keyRune, r: r})
	}
}

func TestTabCompletesNicksMostRecentSpeakerFirstAndCycles(t *testing.T) {
	m := newTestModel("#a")
	m.update(msgIn{event: uiMessage("1", "builder", "#a", "first", 0), history: true})
	m.update(msgIn{event: uiMessage("2", "bob", "#a", "second", time.Second), history: true})
	m.update(msgIn{event: uiMessage("3", "me", "#a", "mine", 2*time.Second), history: true})
	m.update(namesIn{channel: "#a", nicks: []string{"bart", "me"}})

	typeKeys(m, "b")
	m.update(keyIn{kind: keyTab})
	if got := string(m.input); got != "bob: " || m.cursor != len(m.input) {
		t.Fatalf("first Tab = %q (cursor %d), want the newest speaker addressed", got, m.cursor)
	}
	m.update(keyIn{kind: keyTab})
	if got := string(m.input); got != "builder: " {
		t.Fatalf("second Tab = %q, want the next speaker", got)
	}
	m.update(keyIn{kind: keyTab})
	if got := string(m.input); got != "bart: " {
		t.Fatalf("third Tab = %q, want the connected session", got)
	}
	m.update(keyIn{kind: keyBackTab})
	if got := string(m.input); got != "builder: " {
		t.Fatalf("Shift-Tab = %q, want the previous candidate", got)
	}
	if m.current != "#a" {
		t.Fatalf("completing must not switch views, now on %s", m.current)
	}

	// Mid-line, a nick gets a plain space; after @, it keeps the tag.
	typeKeys(m, "see @bo")
	m.update(keyIn{kind: keyTab})
	if got := string(m.input); got != "builder: see @bob " {
		t.Fatalf("tag completion = %q", got)
	}
	m.update(keyIn{kind: keyEsc})
	typeKeys(m, "ask bu")
	m.update(keyIn{kind: keyTab})
	if got := string(m.input); got != "ask builder " {
		t.Fatalf("mid-line completion = %q", got)
	}
}

func TestTabWithNothingTypedStillSwitchesViews(t *testing.T) {
	m := newTestModel("#a", "#b")
	m.update(keyIn{kind: keyTab})
	if m.current != "#b" {
		t.Fatalf("Tab on an empty line should switch views, now on %s", m.current)
	}
	m.update(keyIn{kind: keyBackTab})
	if m.current != "#a" {
		t.Fatalf("Shift-Tab on an empty line should switch back, now on %s", m.current)
	}
	typeKeys(m, "zzz")
	m.update(keyIn{kind: keyTab})
	if m.current != "#a" || string(m.input) != "zzz" {
		t.Fatalf("a word nothing completes must stay put: view %s input %q", m.current, string(m.input))
	}
	if status, isError := m.activeStatus(); !isError || !strings.Contains(status, "zzz") {
		t.Fatalf("status = %q, want a note that zzz completes to nothing", status)
	}
}

func TestTabCompletesCommandsChannelsAndMessageIDs(t *testing.T) {
	m := newTestModel("#alpha", "#beta")
	m.update(msgIn{event: uiMessage("m1", "bob", "#alpha", "hi", 0), history: true})
	typeKeys(m, "/th")
	m.update(keyIn{kind: keyTab})
	if got := string(m.input); got != "/thread " {
		t.Fatalf("command completion = %q", got)
	}
	m.update(keyIn{kind: keyTab}) // a command that takes an ID offers "last"
	if got := string(m.input); got != "/thread last " {
		t.Fatalf("ID completion without a selection = %q", got)
	}
	m.update(keyIn{kind: keyEsc})
	m.find("#alpha").selectedID = "m1"
	typeKeys(m, "/react ")
	m.update(keyIn{kind: keyTab})
	if got := string(m.input); got != "/react m1 " {
		t.Fatalf("ID completion with a selection = %q", got)
	}
	m.update(keyIn{kind: keyEsc})
	typeKeys(m, "/join #b")
	m.update(keyIn{kind: keyTab})
	if got := string(m.input); got != "/join #beta " {
		t.Fatalf("channel completion = %q", got)
	}
	m.update(keyIn{kind: keyEsc})
	typeKeys(m, "/msg b")
	m.update(keyIn{kind: keyTab})
	if got := string(m.input); got != "/msg bob " {
		t.Fatalf("nick completion after a command = %q", got)
	}
	m.update(keyIn{kind: keyEsc})
	typeKeys(m, "/")
	m.update(keyIn{kind: keyTab})
	if got := string(m.input); got != uiCommands[0].name+" " {
		t.Fatalf("a bare slash offers every command, got %q", got)
	}
}

func TestReplyTargetIsPreviewedInTheStatusBar(t *testing.T) {
	m := newTestModel("#a")
	m.update(resizeIn{120, 30})
	m.update(msgIn{event: uiMessage("q1", "planner", "#a", "What timeout\nshould we use?", 0), history: true})
	m.update(keyIn{kind: keySelectPrev})
	m.update(keyIn{kind: keyCtrl, r: 'r'})
	bar := m.statusBar(m.cur(), 120)
	for _, want := range []string{"Reply to q1", "planner: What timeout should we use?", "Esc: cancel"} {
		if !strings.Contains(bar, want) {
			t.Errorf("status bar %q lacks %q", bar, want)
		}
	}
	m.update(keyIn{kind: keyEsc})
	if m.replyTo != "" || strings.Contains(m.statusBar(m.cur(), 120), "Reply to") {
		t.Fatalf("Esc must clear the reply target: %q", m.statusBar(m.cur(), 120))
	}

	// A thread view answers its newest message; say so before Enter.
	m.update(queryIn{name: "thread:q1", page: &uiQuery{Kind: "thread", Target: "q1"}, thread: true,
		messages: []*irc.HistoryEvent{{ID: "q1", From: "planner", Target: "#a", Message: "What timeout", Timestamp: uiBase}, {ID: "r1", From: "bob", Target: "#a", Message: "30s", ThreadID: "q1", ReplyTo: "q1", Timestamp: uiBase.Add(time.Second)}}})
	bar = m.statusBar(m.cur(), 120)
	if !strings.Contains(bar, "Enter replies to r1") || !strings.Contains(bar, "bob: 30s") {
		t.Fatalf("thread status bar = %q", bar)
	}
}

func TestCtrlYInsertsTheSelectedIDAndOffersItToTheClipboard(t *testing.T) {
	m := newTestModel("#a")
	m.update(msgIn{event: uiMessage("m1", "bob", "#a", "hi", 0), history: true})
	typeKeys(m, "/pin ")
	m.update(keyIn{kind: keyCtrl, r: 'y'})
	if string(m.input) != "/pin " || m.clipboard != "" {
		t.Fatalf("without a selection nothing is inserted: %q", string(m.input))
	}
	m.update(keyIn{kind: keySelectPrev})
	m.update(keyIn{kind: keyCtrl, r: 'y'})
	if string(m.input) != "/pin m1" || m.cursor != len(m.input) || m.clipboard != "m1" {
		t.Fatalf("input %q cursor %d clipboard %q", string(m.input), m.cursor, m.clipboard)
	}
	cmds, _ := m.update(keyIn{kind: keyCtrl, r: 't'})
	if len(cmds) != 1 || cmds[0].kind != "thread" || cmds[0].target != "m1" {
		t.Fatalf("Ctrl-T = %+v, want the selected message's thread", cmds)
	}
}

func TestHelpViewReflectsDaemonCapabilities(t *testing.T) {
	m := newTestModel("#a")
	m.update(resizeIn{120, 40})
	typeText(m, "/help")
	help := m.find("help")
	if help == nil || m.current != "help" || help.kind != bufHelp {
		t.Fatalf("/help should open a help view, current %s", m.current)
	}
	text := helpText(help.help)
	if !strings.Contains(text, "Not connected yet") || strings.Contains(text, "unavailable") {
		t.Fatalf("before the daemon speaks nothing is unavailable:\n%s", text)
	}
	for _, c := range uiCommands {
		if !strings.Contains(text, "\n"+c.name+" ") && !strings.Contains(text, "\n"+c.name+"\n") {
			t.Errorf("help lacks %s", c.name)
		}
	}
	m.update(capabilitiesIn{features: map[string]string{"CHAT": "1", "REPLIES": "1", "SEARCH": "1", "TOPIC": "1"}})
	text = helpText(m.find("help").help)
	if strings.Contains(text, "Not connected") {
		t.Fatalf("the daemon has spoken:\n%s", text)
	}
	for line := range strings.SplitSeq(text, "\n") {
		switch {
		case strings.HasPrefix(line, "/pin "), strings.HasPrefix(line, "/thread "), strings.HasPrefix(line, "/quit"):
			if strings.Contains(line, "unavailable") {
				t.Errorf("supported command marked unavailable: %s", line)
			}
		case strings.HasPrefix(line, "/context "), strings.HasPrefix(line, "/mute "), strings.HasPrefix(line, "/op "):
			if !strings.Contains(line, "unavailable") {
				t.Errorf("unsupported command not marked: %s", line)
			}
		}
	}
	for _, width := range []int{120, 70, 30} {
		m.update(resizeIn{width, 40})
		for _, row := range m.view() {
			if visibleWidth(row) != width {
				t.Fatalf("help row is %d wide at %d columns: %q", visibleWidth(row), width, row)
			}
		}
	}
	m.update(resizeIn{120, 40})
	if rendered := strings.Join(m.logLines(m.find("help"), 100), "\n"); !strings.Contains(rendered, "/thread ID") || !strings.Contains(rendered, "Conversations") {
		t.Fatalf("rendered help:\n%s", rendered)
	}
	if cmds, _ := typeText(m, "hello"); len(cmds) != 0 {
		t.Fatalf("typing in help must not send: %+v", cmds)
	}
	typeText(m, "/close")
	if m.current != "#a" || m.find("help") != nil {
		t.Fatalf("/close should return to #a, current %s", m.current)
	}
}

func TestUpAndDownRecallSentLinesOrScroll(t *testing.T) {
	m := newTestModel("#alpha")
	m.update(resizeIn{100, 10})
	for i := range 30 {
		m.update(msgIn{event: uiMessage(fmt.Sprint("m", i), "bob", "#alpha", "line", time.Duration(i)*time.Second)})
	}
	m.view()
	up, down := keyIn{kind: keyUp}, keyIn{kind: keyDown}
	m.update(up)
	if m.find("#alpha").scroll != 1 {
		t.Fatal("with nothing sent, Up scrolls the log")
	}
	m.update(down)
	typeText(m, "first line")
	typeText(m, "/topic second")
	typeText(m, "/topic second") // a repeat of the newest line is remembered once
	m.update(up)
	if string(m.input) != "/topic second" || m.cursor != len(m.input) {
		t.Fatalf("Up recalled %q at %d", string(m.input), m.cursor)
	}
	m.update(up)
	if string(m.input) != "first line" {
		t.Fatalf("second Up recalled %q", string(m.input))
	}
	m.update(up)
	if string(m.input) != "first line" || m.find("#alpha").scroll != 0 {
		t.Fatalf("Up past the oldest line changed %q or scrolled %d", string(m.input), m.find("#alpha").scroll)
	}
	m.update(down)
	m.update(down)
	if len(m.input) != 0 {
		t.Fatalf("Down past the newest line left %q", string(m.input))
	}
	m.update(down)
	if m.find("#alpha").scroll != 0 {
		t.Fatal("Down on an empty composer scrolls, and the log is at the newest")
	}
	// An edited recall is the user's own draft again, so Up scrolls instead.
	m.update(up)
	m.update(keyIn{kind: keyRune, r: '!'})
	m.update(up)
	if string(m.input) != "/topic second!" || m.find("#alpha").scroll != 1 {
		t.Fatalf("Up on an edited line left %q, scroll %d", string(m.input), m.find("#alpha").scroll)
	}
	m.update(keyIn{kind: keyEsc})
	m.update(down)
	// On a multi-line recall only the first line goes back, and only the last forward.
	history := m.find("#alpha").history
	history[len(history)-1] = "one\ntwo"
	m.update(up)
	if string(m.input) != "one\ntwo" {
		t.Fatalf("Up recalled %q", string(m.input))
	}
	m.update(up)
	if string(m.input) != "one\ntwo" || m.find("#alpha").scroll != 1 {
		t.Fatalf("Up on the second line of a recall left %q, scroll %d", string(m.input), m.find("#alpha").scroll)
	}
	m.cursor = 0
	m.update(down)
	if string(m.input) != "one\ntwo" || m.find("#alpha").scroll != 0 {
		t.Fatalf("Down on the first line of a recall left %q, scroll %d", string(m.input), m.find("#alpha").scroll)
	}
	m.update(up)
	if string(m.input) != "first line" {
		t.Fatalf("Up on the first line recalled %q", string(m.input))
	}
	// History is per view and bounded.
	m.update(keyIn{kind: keyEsc})
	m.switchTo("@me")
	m.update(up)
	if len(m.input) != 0 {
		t.Fatalf("another view recalled %q", string(m.input))
	}
	m.switchTo("#alpha")
	for i := range historyLimit + 5 {
		typeText(m, fmt.Sprint("n", i))
	}
	if n := len(m.find("#alpha").history); n != historyLimit {
		t.Fatalf("history holds %d lines", n)
	}
}
