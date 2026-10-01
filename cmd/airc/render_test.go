package main

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Someblueman/airc/pkg/irc"
)

var sgr = regexp.MustCompile("\x1b\\[[0-9;]*m")

func visible(text string) string { return sgr.ReplaceAllString(text, "") }

func stamp(h, m, s int) time.Time { return time.Date(2026, 10, 1, h, m, s, 0, time.Local) }

func msg(at time.Time, from, target, body string) *irc.MessageEvent {
	return &irc.MessageEvent{Type: "message", From: from, Target: target, Message: body, Timestamp: at}
}

func TestPlainRenderingIsAlignedClassicIRC(t *testing.T) {
	r := newRenderer(false, 80, false)
	got := r.render(msg(stamp(12, 34, 56), "alice", "#room", "hello"))
	want := "--- Thu 01 Oct 2026 ---\n12:34:56      <alice> hello\n"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	// Same day: no second rule. A longer nick widens the column for what follows.
	got = r.render(msg(stamp(12, 35, 0), "researcher", "#room", "first\nsecond"))
	want = "\n12:35:00 <researcher> first\n" + strings.Repeat(" ", len("12:35:00 <researcher> ")) + "second\n"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	if strings.ContainsRune(got, '\x1b') {
		t.Fatal("plain output contains escape sequences")
	}
}

func TestDayRuleReappearsWhenTheDateChanges(t *testing.T) {
	r := newRenderer(false, 80, false)
	r.render(msg(stamp(23, 59, 59), "a", "#r", "late"))
	next := time.Date(2026, 10, 2, 0, 0, 1, 0, time.Local)
	if got := r.render(msg(next, "a", "#r", "early")); !strings.Contains(got, "--- Fri 02 Oct 2026 ---") {
		t.Fatalf("no day separator: %q", got)
	}
}

func TestWrappingKeepsIndentAndHangsListItems(t *testing.T) {
	r := newRenderer(false, 60, false)
	body := "Plan:\n  - " + strings.Repeat("word ", 20) + "\n1. " + strings.Repeat("step ", 20) + "\n\nend"
	out := r.render(msg(stamp(1, 2, 3), "bot", "#r", body))
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if utf8.RuneCountInString(line) > 60 {
			t.Errorf("line exceeds width (%d): %q", utf8.RuneCountInString(line), line)
		}
	}
	// time (8) + space + nick column (10 wide, plus <>) + space
	indent := strings.Repeat(" ", 8+1+10+2+1)
	if !strings.Contains(out, "\n"+indent+"  - word") {
		t.Errorf("list item lost its indentation:\n%s", out)
	}
	if !strings.Contains(out, "\n"+indent+"   step") {
		t.Errorf("wrapped numbered item does not hang under its text:\n%s", out)
	}
	if !strings.Contains(out, "\n\n") {
		t.Errorf("blank line inside the message was dropped:\n%s", out)
	}
}

func TestVeryLongWordsAreHardWrapped(t *testing.T) {
	r := newRenderer(false, 50, false)
	out := r.render(msg(stamp(1, 2, 3), "bot", "#r", "x "+strings.Repeat("y", 300)+" z"))
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if utf8.RuneCountInString(line) > 50 {
			t.Fatalf("overlong line: %q", line)
		}
	}
	if strings.Count(out, "y") != 300 {
		t.Fatalf("wrapping lost characters: %d y's", strings.Count(out, "y"))
	}
}

func TestControlCharactersCannotReachTheTerminal(t *testing.T) {
	for _, color := range []bool{false, true} {
		r := newRenderer(color, 80, false)
		evil := "\x1b[2J\x1b]0;pwned\x07red\x1b[31m text \u202eevil\u009b31m\x00 end"
		out := r.render(msg(stamp(1, 2, 3), "ev\x1bil", "#r", evil))
		if strings.ContainsAny(visible(out), "\x1b\x07\x00\u009b\u202e") {
			t.Fatalf("color=%v: unsafe characters survived: %q", color, out)
		}
		if !strings.Contains(visible(out), "evil") {
			t.Fatalf("color=%v: legitimate text was removed: %q", color, out)
		}
	}
}

func TestColorRenderingStylesNicksAndMarkdown(t *testing.T) {
	r := newRenderer(true, 80, false)
	out := r.render(msg(stamp(9, 8, 7), "planner", "#r",
		"builder: see `go test` and **this** at https://example.com/x\n# Heading\n- item one"))
	if !sgr.MatchString(out) {
		t.Fatal("no styling applied")
	}
	plain := visible(out)
	for _, unwanted := range []string{"`", "**", "# Heading"} {
		if strings.Contains(plain, unwanted) {
			t.Errorf("markdown marker %q left in output: %q", unwanted, plain)
		}
	}
	for _, wanted := range []string{"go test", "this", "https://example.com/x", "Heading", "• item one", "builder:"} {
		if !strings.Contains(plain, wanted) {
			t.Errorf("missing %q in %q", wanted, plain)
		}
	}
	if strings.Count(out, "\x1b[0m") < strings.Count(out, "\x1b[")/2 {
		t.Error("styles are not all closed on the line they open")
	}
	if nickColor("Alice") != nickColor("alice") {
		t.Error("nick color must not depend on case")
	}
	distinct := map[int]bool{}
	for _, n := range []string{"planner", "builder", "reviewer", "researcher", "musespark", "trinode"} {
		distinct[nickColor(n)] = true
	}
	if len(distinct) < 4 {
		t.Errorf("nick colors collide too much: %d distinct", len(distinct))
	}
}

func TestColorOutputNeverExceedsTheWidth(t *testing.T) {
	r := newRenderer(true, 70, false)
	body := "**" + strings.Repeat("bold ", 5) + "** " + strings.Repeat("`code` and text ", 10) + "\n- " + strings.Repeat("item ", 30)
	for _, line := range strings.Split(strings.TrimRight(visible(r.render(msg(stamp(1, 1, 1), "bot", "#r", body))), "\n"), "\n") {
		if utf8.RuneCountInString(line) > 70 {
			t.Fatalf("visible line exceeds width: %q", line)
		}
	}
}

func TestDirectMessagesAndMultipleTargetsAreLabelled(t *testing.T) {
	r := newRenderer(false, 80, true)
	dm := r.render(msg(stamp(1, 2, 3), "alice", "bob", "psst"))
	if !strings.Contains(dm, "<alice> [dm -> bob] psst") {
		t.Fatalf("dm = %q", dm)
	}
	room := r.render(msg(stamp(1, 2, 4), "alice", "#ops", "deploy"))
	if !strings.Contains(room, "01:02:04 #ops ") || !strings.Contains(room, "<alice> deploy") {
		t.Fatalf("room = %q", room)
	}
}

func TestPresenceAndConnectionNotices(t *testing.T) {
	r := newRenderer(false, 80, false)
	r.lastDay = stamp(1, 1, 1).Local().Format("Mon 02 Jan 2006")
	cases := map[string]irc.Event{
		"-->": &irc.JoinEvent{Agent: "alice", Channel: "#room", Timestamp: stamp(1, 2, 3)},
		"<--": &irc.PartEvent{Agent: "alice", Channel: "#room", Reason: "bye"},
	}
	for marker, event := range cases {
		out := r.render(event)
		if !strings.Contains(out, marker+" alice ") {
			t.Errorf("%T rendered %q", event, out)
		}
	}
	if out := r.render(&irc.QuitEvent{Agent: "bob", Reason: "Client closed"}); !strings.Contains(out, "<-- bob quit (Client closed)") {
		t.Errorf("quit = %q", out)
	}
	if out := r.disconnected(); !strings.Contains(out, "*** disconnected from server") {
		t.Errorf("disconnected = %q", out)
	}
	if got := r.render(&irc.PresenceEvent{}); got != "" {
		t.Errorf("unrelated events should render nothing, got %q", got)
	}
}

func TestBannerAndOptions(t *testing.T) {
	if got := newRenderer(false, 80, false).banner([]string{"#a", "@me"}); got != "*** Watching #a, @me (hidden, read-only). Ctrl-C to stop.\n" {
		t.Fatalf("plain banner = %q", got)
	}
	if got := visible(newRenderer(true, 80, false).banner([]string{"#a"})); !strings.Contains(got, "watching #a") {
		t.Fatalf("color banner = %q", got)
	}
	var notTerminal strings.Builder
	if useColor("auto", &notTerminal) || !useColor("always", &notTerminal) || useColor("never", &notTerminal) {
		t.Fatal("color mode selection is wrong")
	}
	if outputWidth(50, &notTerminal) != 50 {
		t.Fatal("--width must be used as given")
	}
	t.Setenv("COLUMNS", "90")
	if got := outputWidth(0, &notTerminal); got != 89 {
		t.Fatalf("width from COLUMNS = %d", got)
	}
	t.Setenv("COLUMNS", "500")
	if got := outputWidth(0, &notTerminal); got != 120 {
		t.Fatalf("width should be capped, got %d", got)
	}
}

func TestStyledSpansSurviveWrappingAndNeverLeakAcrossLines(t *testing.T) {
	r := newRenderer(true, 50, false)
	body := "run `" + strings.Repeat("alpha ", 12) + "` now and **" + strings.Repeat("loud ", 12) + "** done"
	out := r.render(msg(stamp(1, 1, 1), "bot", "#r", body))
	if strings.ContainsAny(visible(out), "`") || strings.Contains(visible(out), "**") {
		t.Fatalf("markers left in wrapped output:\n%s", visible(out))
	}
	codeLines, boldLines := 0, 0
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if strings.Contains(line, "\x1b[38;5;221m") {
			codeLines++
		}
		if strings.Contains(line, "\x1b[1m") && strings.Contains(visible(line), "loud") {
			boldLines++
		}
		// A line must never end with a style still switched on.
		last := sgr.FindAllString(line, -1)
		if len(last) > 0 && last[len(last)-1] != "\x1b[0m" {
			t.Errorf("line leaves a style open: %q", line)
		}
		if utf8.RuneCountInString(visible(line)) > 50 {
			t.Errorf("line exceeds width: %q", visible(line))
		}
	}
	if codeLines < 2 || boldLines < 2 {
		t.Fatalf("spans should be styled on every line they cover: code=%d bold=%d\n%q", codeLines, boldLines, out)
	}
}

func TestMessagesCannotForgeStyleMarks(t *testing.T) {
	for _, color := range []bool{false, true} {
		r := newRenderer(color, 80, false)
		out := r.render(msg(stamp(1, 1, 1), "bot", "#r", "a forged x"))
		if strings.ContainsAny(out, "") {
			t.Fatalf("color=%v: a style mark survived: %q", color, out)
		}
		if strings.Contains(out, "38;5;221") {
			t.Fatalf("color=%v: forged marks produced styling: %q", color, out)
		}
	}
}

func TestUnmatchedMarkersStayLiteral(t *testing.T) {
	r := newRenderer(true, 80, false)
	got := visible(r.render(msg(stamp(1, 1, 1), "bot", "#r", "a lone ` tick and ** stars and 2 * 3")))
	if !strings.Contains(got, "a lone ` tick and ** stars and 2 * 3") {
		t.Fatalf("unmatched markers were altered: %q", got)
	}
}

func TestWrappedBulletsHangUnderTheirTextInColorMode(t *testing.T) {
	r := newRenderer(true, 50, false)
	out := visible(r.render(msg(stamp(1, 1, 1), "bot", "#r", "- "+strings.Repeat("word ", 20))))
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	textColumn := strings.Index(lines[1], "•") + 2
	for _, line := range lines[2:] {
		if !strings.HasPrefix(line, strings.Repeat(" ", textColumn)+"word") {
			t.Fatalf("continuation does not hang under the bullet text (want %d spaces): %q", textColumn, line)
		}
	}
}

func TestNickColumnStaysFixedWhenALongerNickAppearsLater(t *testing.T) {
	r := newRenderer(false, 100, false)
	for _, nick := range []string{"anvil", "Kestrel", "researcher-12"} { // what a backlog reveals up front
		r.reserve(nick)
	}
	first := r.render(msg(stamp(1, 1, 1), "anvil", "#r", "hello"))
	last := r.render(msg(stamp(1, 1, 2), "researcher-12", "#r", "hello"))
	// Compare the text start positions directly: they must match line for line.
	startOf := func(out string) int {
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		return strings.Index(lines[len(lines)-1], "hello")
	}
	if startOf(first) != startOf(last) {
		t.Fatalf("text starts at column %d then %d:\n%s%s", startOf(first), startOf(last), first, last)
	}
}

func TestLongMessagesAreSeparatedButShortChatStaysCompact(t *testing.T) {
	r := newRenderer(false, 60, false)
	short1 := r.render(msg(stamp(1, 1, 1), "a", "#r", "one"))
	short2 := r.render(msg(stamp(1, 1, 2), "b", "#r", "two"))
	if strings.Contains(strings.TrimPrefix(short1, "--- Thu 01 Oct 2026 ---\n")+short2, "\n\n") {
		t.Fatalf("short messages should stay compact:\n%s%s", short1, short2)
	}
	long := r.render(msg(stamp(1, 1, 3), "c", "#r", strings.Repeat("word ", 30)))
	if !strings.HasPrefix(long, "\n") {
		t.Fatalf("a long message should be set off from the previous one: %q", long)
	}
	after := r.render(msg(stamp(1, 1, 4), "d", "#r", "short again"))
	if !strings.HasPrefix(after, "\n") {
		t.Fatalf("the message after a long one should be set off: %q", after)
	}
	if rule := r.rule("live"); !strings.HasPrefix(rule, "--- live") {
		t.Fatalf("rule = %q", rule)
	}
	if first := r.render(msg(stamp(1, 1, 5), "e", "#r", "after the rule")); strings.HasPrefix(first, "\n") {
		t.Fatalf("no blank line is needed after a rule: %q", first)
	}
}

func TestTaggedNicknamesAreColoredLikeTheirOwner(t *testing.T) {
	r := newRenderer(true, 100, false)
	out := r.render(msg(stamp(1, 1, 1), "planner", "#r", "thanks @anvil and (@Kestrel), mail a@b.com or `@code`"))
	for _, nick := range []string{"anvil", "Kestrel"} {
		want := fmt.Sprintf("\x1b[1;38;5;%dm@%s\x1b[0m", nickColor(nick), nick)
		if !strings.Contains(out, want) {
			t.Errorf("@%s is not styled with that nick's color %q:\n%q", nick, want, out)
		}
	}
	plain := visible(out)
	if !strings.Contains(plain, "mail a@b.com or @code") {
		t.Errorf("an email address or code span was altered: %q", plain)
	}
	if strings.Contains(out, fmt.Sprintf("1;38;5;%dm@code", nickColor("code"))) {
		t.Error("an @ inside a code span must not be treated as a tag")
	}
}

func TestTaggedNicknameStaysStyledAcrossAWrap(t *testing.T) {
	r := newRenderer(true, 40, false)
	out := r.render(msg(stamp(1, 1, 1), "planner", "#r", strings.Repeat("x ", 9)+"@verylongagentnamethatwraps tail"))
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		codes := sgr.FindAllString(line, -1)
		if len(codes) > 0 && codes[len(codes)-1] != "\x1b[0m" {
			t.Errorf("line leaves a style open: %q", line)
		}
	}
	if strings.ContainsAny(visible(out), "\ue006\ue007\ue100\ue101\ue102\ue103") {
		t.Errorf("style marks leaked into the output: %q", out)
	}
}

func TestStyleMarksForTagsCannotBeForged(t *testing.T) {
	r := newRenderer(true, 80, false)
	out := r.render(msg(stamp(1, 1, 1), "bot", "#r", "a \ue006\ue105fake\ue007 mention"))
	if strings.ContainsAny(out, "\ue006\ue007\ue105") || strings.Contains(out, "1;38;5;") {
		t.Fatalf("forged mention marks survived: %q", out)
	}
}
