package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Someblueman/airc/pkg/irc"
)

var ansiPattern = regexp.MustCompile("\x1b\\[[0-9;?]*[A-Za-z]")

func visibleWidth(s string) int { return utf8.RuneCountInString(ansiPattern.ReplaceAllString(s, "")) }

// padVisible pads s with spaces to w visible columns, clipping if it is wider.
func padVisible(s string, w int) string {
	width := visibleWidth(s)
	if width > w {
		return clipVisible(s, w)
	}
	return s + strings.Repeat(" ", w-width)
}

// clipVisible cuts s after w visible columns, keeping escape sequences intact and
// closing any style left open.
func clipVisible(s string, w int) string {
	var b strings.Builder
	shown := 0
	for i := 0; i < len(s); {
		if loc := ansiPattern.FindStringIndex(s[i:]); loc != nil && loc[0] == 0 {
			b.WriteString(s[i : i+loc[1]])
			i += loc[1]
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if shown >= w {
			break
		}
		b.WriteRune(r)
		shown++
		i += size
	}
	b.WriteString("\x1b[0m")
	return b.String()
}

// fitPlain truncates plain text to w columns with an ellipsis, then pads it.
func fitPlain(s string, w int) string {
	if w <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) > w {
		runes = append(runes[:max(w-1, 0)], '…')
	}
	return string(runes) + strings.Repeat(" ", w-len(runes))
}

func sty(codes, text string) string {
	if text == "" {
		return text
	}
	return "\x1b[" + codes + "m" + text + "\x1b[0m"
}

// layout chooses pane widths: both side panes on wide terminals, fewer as it narrows.
func (m *uiModel) layout() (left, right, center int) {
	switch w := m.width; {
	case w >= 100:
		left, right = 24, 22
	case w >= 80:
		left, right = 20, 18
	case w >= 60:
		left = 18
	}
	separators := 0
	if left > 0 {
		separators++
	}
	if right > 0 {
		separators++
	}
	return left, right, max(m.width-left-right-separators, 10)
}

// view draws the whole screen: exactly height rows of exactly width columns.
func (m *uiModel) view() []string {
	width, height := max(m.width, 30), max(m.height, 6)
	m.width, m.height = width, height
	left, right, center := m.layout()
	bodyHeight := height - 3
	b := m.cur()

	// One column of gutter keeps the text off the pane dividers.
	gutter := 0
	if left > 0 || right > 0 {
		gutter = 1
	}
	logLines := m.logLines(b, center-gutter)
	maxScroll := max(len(logLines)-bodyHeight, 0)
	b.scroll = min(b.scroll, maxScroll)
	end := len(logLines) - b.scroll
	visible := logLines[max(end-bodyHeight, 0):end]

	var leftCells, rightCells []string
	if left > 0 {
		leftCells = m.channelPane(bodyHeight, left)
	}
	if right > 0 {
		rightCells = m.memberPane(b, bodyHeight, right)
	}
	separator := sty("2", "│")

	rows := make([]string, 0, height)
	rows = append(rows, m.header(b, width))
	for i := 0; i < bodyHeight; i++ {
		var row strings.Builder
		if left > 0 {
			row.WriteString(leftCells[i] + separator)
		}
		line := ""
		if i < len(visible) {
			line = visible[i]
		}
		row.WriteString(padVisible(strings.Repeat(" ", gutter)+line, center))
		if right > 0 {
			row.WriteString(separator + rightCells[i])
		}
		rows = append(rows, row.String())
	}
	rows = append(rows, m.statusBar(b, width))
	input, _ := m.inputRow(width)
	return append(rows, input)
}

func (m *uiModel) header(b *uiBuffer, width int) string {
	state := "○ offline "
	if m.connected {
		state = "● online "
	}
	title := " " + b.name
	if b.kind == bufInbox {
		title += "  mentions and direct messages"
	} else if b.kind == bufDirectMessages {
		title = " All DMs  │  " + b.topic
	} else if b.topic != "" {
		title += "  │  " + b.topic
	}
	title = fitPlain(title, max(width-utf8.RuneCountInString(state), 1))
	return sty("7;1", padVisible(title+state, width))
}

func (m *uiModel) statusBar(b *uiBuffer, width int) string {
	if text, isError := m.activeStatus(); text != "" {
		codes := "2"
		if isError {
			codes = "1;38;5;203"
		}
		return sty(codes, fitPlain(" "+text, width))
	}
	if b.scroll > 0 {
		return sty("38;5;221", fitPlain(fmt.Sprintf(" ↑ scrolled back %d lines · PgDn for the newest", b.scroll), width))
	}
	return sty("2", fitPlain(" Tab: next channel · PgUp/PgDn: scroll · /help", width))
}

// inputRow draws the prompt and typed text, scrolled so the cursor stays visible,
// and reports the 1-based screen column of the cursor.
func (m *uiModel) inputRow(width int) (string, int) {
	prompt := "[" + m.nick + "] "
	room := max(width-utf8.RuneCountInString(prompt)-1, 1)
	start := max(m.cursor-room+1, 0)
	shown := m.input[start:min(len(m.input), start+room)]
	row := sty("1;2", prompt) + string(shown)
	return padVisible(row, width), utf8.RuneCountInString(prompt) + (m.cursor - start) + 1
}

// cursorPosition is where the terminal cursor belongs: the end of the input row.
func (m *uiModel) cursorPosition() (row, col int) {
	_, col = m.inputRow(m.width)
	return m.height, col
}

func (m *uiModel) logLines(b *uiBuffer, width int) []string {
	if b.cacheLines != nil && b.cacheWidth == width && b.cacheVersion == b.version {
		return b.cacheLines
	}
	view := newRenderer(true, width, b.kind != bufChannel)
	for _, event := range b.items {
		if message, ok := event.(*irc.MessageEvent); ok {
			view.reserve(message.From)
		}
	}
	var lines []string
	for _, event := range b.items {
		if text := strings.TrimRight(view.render(event), "\n"); text != "" {
			lines = append(lines, strings.Split(text, "\n")...)
		}
	}
	if len(lines) == 0 {
		lines = []string{sty("2", " Nothing here yet.")}
	}
	b.cacheWidth, b.cacheVersion, b.cacheLines = width, b.version, lines
	return lines
}

func (m *uiModel) channelPane(rows, width int) []string {
	type entry struct {
		text    string
		buffer  *uiBuffer
		heading bool
	}
	var entries []entry
	entries = append(entries, entry{text: "Channels", heading: true})
	for _, b := range m.buffers {
		if b.kind == bufInbox {
			entries = append(entries, entry{}, entry{text: "Inbox", heading: true})
		} else if b.kind == bufDirectMessages {
			entries = append(entries, entry{}, entry{text: "Human oversight", heading: true})
		}
		entries = append(entries, entry{buffer: b})
	}
	selected := 0
	for i, e := range entries {
		if e.buffer != nil && e.buffer.name == m.current {
			selected = i
		}
	}
	first := 0
	if len(entries) > rows {
		first = min(max(selected-rows/2, 0), len(entries)-rows)
	}
	cells := make([]string, 0, rows)
	for i := first; i < len(entries) && len(cells) < rows; i++ {
		e := entries[i]
		switch {
		case e.heading:
			cells = append(cells, sty("1;2", fitPlain(" "+e.text, width)))
		case e.buffer == nil:
			cells = append(cells, strings.Repeat(" ", width))
		default:
			b := e.buffer
			badge := ""
			if b.unread > 0 {
				badge = fmt.Sprintf("%d", b.unread)
				if b.mention {
					badge += "!"
				}
			}
			room := max(width-utf8.RuneCountInString(badge)-2, 1)
			name := b.name
			if b.kind == bufDirectMessages {
				name = "All DMs"
			}
			text := " " + fitPlain(name, room) + " "
			text = fitPlain(text, width-utf8.RuneCountInString(badge)) + badge
			switch {
			case b.name == m.current:
				text = sty("7", text)
			case b.mention:
				text = sty("1;38;5;203", text)
			case b.unread > 0:
				text = sty("1", text)
			}
			cells = append(cells, text)
		}
	}
	for len(cells) < rows {
		cells = append(cells, strings.Repeat(" ", width))
	}
	return cells
}

type participant struct {
	nick string
	last time.Time
	live bool
}

// participants lists who is in a channel: sessions connected right now, and
// everyone who has spoken in it. One-shot agents are never "connected", so
// recent speakers are what tells you who is around.
func (m *uiModel) participants(b *uiBuffer) []participant {
	byNick := map[string]*participant{}
	get := func(nick string) *participant {
		key := strings.ToLower(nick)
		if byNick[key] == nil {
			byNick[key] = &participant{nick: nick}
		}
		return byNick[key]
	}
	for _, nick := range b.live {
		get(nick).live = true
	}
	for _, event := range b.items {
		if message, ok := event.(*irc.MessageEvent); ok {
			if p := get(message.From); message.Timestamp.After(p.last) {
				p.last = message.Timestamp
			}
		}
	}
	out := make([]participant, 0, len(byNick))
	for _, p := range byNick {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].live != out[j].live {
			return out[i].live
		}
		// Connected sessions keep a stable alphabetical order; speakers who are
		// merely around come newest first.
		if !out[i].live && !out[i].last.Equal(out[j].last) {
			return out[i].last.After(out[j].last)
		}
		return strings.ToLower(out[i].nick) < strings.ToLower(out[j].nick)
	})
	return out
}

func age(since, now time.Time) string {
	d := now.Sub(since)
	switch {
	case since.IsZero():
		return ""
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func (m *uiModel) memberPane(b *uiBuffer, rows, width int) []string {
	people := m.participants(b)
	cells := []string{sty("1;2", fitPlain(fmt.Sprintf(" Members (%d)", len(people)), width))}
	for i, p := range people {
		if len(cells) >= rows {
			break
		}
		if len(cells) == rows-1 && i < len(people)-1 {
			cells = append(cells, sty("2", fitPlain(fmt.Sprintf(" +%d more", len(people)-i), width)))
			break
		}
		mark, suffix := " ", ""
		if p.live {
			mark = sty("38;5;41", "●")
		}
		if !p.live {
			suffix = age(p.last, m.now())
		}
		name := p.nick
		if strings.EqualFold(p.nick, m.nick) {
			name += " (you)"
		}
		room := max(width-2-utf8.RuneCountInString(suffix)-1, 1)
		label := fitPlain(name, room)
		cells = append(cells, padVisible(" "+mark+" "+sty("38;5;"+fmt.Sprint(nickColor(p.nick)), label)+sty("2", suffix), width))
	}
	for len(cells) < rows {
		cells = append(cells, strings.Repeat(" ", width))
	}
	return cells
}
