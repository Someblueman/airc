package main

import (
	"fmt"
	"hash/fnv"
	"os"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
	"unsafe"

	"github.com/Someblueman/airc/pkg/irc"
)

// nickPalette holds 256-color codes that stay readable on both dark and light
// terminals. A nickname always maps to the same entry.
var nickPalette = []int{38, 208, 41, 170, 141, 178, 43, 204, 75, 112, 177, 209, 71, 135, 172, 37}

// Inline styles are marked with zero-width private-use runes while text is
// wrapped, then turned into ANSI sequences afterwards so a span that wraps onto
// several lines stays styled on each of them. cleanText removes these runes from
// incoming text, so a message cannot produce them itself.
const (
	markCodeOn  = '\ue000'
	markCodeOff = '\ue001'
	markBoldOn  = '\ue002'
	markBoldOff = '\ue003'
	markLinkOn  = '\ue004'
	markLinkOff = '\ue005'

	markMentionOn  = '\ue006' // followed by one rune from markColorBase naming the nick's palette entry
	markMentionOff = '\ue007'
	markColorBase  = '\ue100'
)

func isMark(c rune) bool { return c >= '\ue000' && c <= '\ue1ff' }

const (
	maxNickColumn = 16
	maxRoomColumn = 24
	minBodyWidth  = 8
	// compactBelow is how much room the text must have before the full layout is
	// used; narrower than that, a compact layout gives the text more of the line.
	compactBelow = 24
	timeColumn   = len("15:04:05")
)

// renderer turns events into IRC-style log lines:
//
//	15:04:05      <alice> builder: please implement task 7
//	15:04:06          --> bob joined #room
//
// The nickname column is right-aligned and wrapped text hangs under the message,
// as in irssi or weechat. With color off the output is plain ASCII, so piped
// logs stay greppable.
type renderer struct {
	color     bool
	width     int
	showRoom  bool // prefix each message with its channel; used when watching several targets
	nickWidth int
	roomWidth int
	lastDay   string

	// A blank line separates messages when either is longer than one line, so
	// long paragraphs do not run together while short chat stays compact.
	afterMessage bool
	prevTall     bool
}

// reserve widens the nickname column for a name before anything is printed, so
// a longer name appearing later does not shift the text of earlier lines.
func (r *renderer) reserve(nick string) {
	r.nickWidth = max(r.nickWidth, min(utf8.RuneCountInString(cleanText(nick)), maxNickColumn))
}

func newRenderer(color bool, width int, showRoom bool) *renderer {
	if width < 24 {
		width = 24
	}
	return &renderer{color: color, width: width, showRoom: showRoom, nickWidth: 10}
}

func (r *renderer) style(text string, codes string) string {
	if !r.color || text == "" {
		return text
	}
	return "\x1b[" + codes + "m" + text + "\x1b[0m"
}

func (r *renderer) dim(text string) string  { return r.style(text, "2") }
func (r *renderer) bold(text string) string { return r.style(text, "1") }
func (r *renderer) fg(code int, text string) string {
	return r.style(text, fmt.Sprintf("38;5;%d", code))
}

func nickIndex(nick string) int {
	hash := fnv.New32a()
	hash.Write([]byte(strings.ToLower(nick)))
	return int(hash.Sum32()) % len(nickPalette)
}

func nickColor(nick string) int { return nickPalette[nickIndex(nick)] }

func (r *renderer) banner(targets []string) string {
	if !r.color {
		return fmt.Sprintf("*** Watching %s (hidden, read-only). Ctrl-C to stop.\n", strings.Join(targets, ", "))
	}
	return r.bold(r.fg(45, "***")) + " watching " + r.bold(strings.Join(targets, ", ")) + r.dim("  hidden, read-only · Ctrl-C to stop") + "\n"
}

// render returns the text to print for an event, or "" for events it ignores.
func (r *renderer) render(event irc.Event) string {
	switch e := event.(type) {
	case *irc.MessageEvent:
		return r.message(e)
	case *irc.JoinEvent:
		return r.notice(e.Timestamp, "-->", 41, fmt.Sprintf("%s joined %s", cleanText(e.Agent), cleanText(e.Channel)))
	case *irc.KickEvent:
		return r.notice(time.Now(), "<--", 203, fmt.Sprintf("%s kicked %s from %s (%s)", cleanText(e.By), cleanText(e.Agent), cleanText(e.Channel), cleanText(e.Reason)))
	case *irc.PartEvent:
		text := fmt.Sprintf("%s left %s", cleanText(e.Agent), cleanText(e.Channel))
		if e.Reason != "" {
			text += " (" + cleanText(e.Reason) + ")"
		}
		return r.notice(time.Now(), "<--", 203, text)
	case *irc.QuitEvent:
		text := fmt.Sprintf("%s quit", cleanText(e.Agent))
		if e.Reason != "" {
			text += " (" + cleanText(e.Reason) + ")"
		}
		return r.notice(time.Now(), "<--", 203, text)
	case *irc.TopicEvent:
		channel := cleanText(e.Channel)
		switch {
		case e.SetBy != "" && e.Topic == "":
			return r.notice(time.Now(), "***", 45, cleanText(e.SetBy)+" cleared the topic of "+channel)
		case e.SetBy != "":
			return r.notice(time.Now(), "***", 45, cleanText(e.SetBy)+" set the topic of "+channel+": "+cleanText(e.Topic))
		case e.Topic == "":
			return ""
		default:
			return r.notice(time.Now(), "***", 45, "Topic of "+channel+": "+cleanText(e.Topic))
		}
	case *irc.ConnectionEvent:
		if e.Connected {
			return r.notice(time.Now(), "***", 41, "reconnected")
		}
		return r.notice(time.Now(), "***", 203, "connection lost: "+cleanText(e.Error))
	}
	return ""
}

// rule is a dim horizontal label, such as the line between backlog and live traffic.
func (r *renderer) rule(text string) string {
	r.afterMessage = false
	if r.color {
		return r.dim("── "+text+" ──") + "\n"
	}
	return "--- " + text + " ---\n"
}

// info is a status line; good marks a recovery rather than a problem.
func (r *renderer) info(text string, good bool) string {
	color := 178
	if good {
		color = 41
	}
	return r.notice(time.Time{}, "***", color, text)
}

// disconnected is shown when the server closes the stream.
func (r *renderer) disconnected() string {
	return r.notice(time.Now(), "***", 203, "disconnected from server")
}

// ellipsize shortens s to at most n runes, marking the cut with "…".
func ellipsize(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:max(n-1, 0)]) + "…"
}

func (r *renderer) message(m *irc.MessageEvent) string {
	if m.Reaction != "" {
		return r.notice(m.Timestamp, "***", 45, cleanText(m.Target)+" "+cleanText(m.From)+" reacted "+cleanText(m.Reaction)+" to "+cleanText(m.ReplyTo))
	}
	from := cleanText(m.From)
	from = ellipsize(from, maxNickColumn)
	if n := utf8.RuneCountInString(from); n > r.nickWidth {
		r.nickWidth = n
	}
	room, dm := "", ""
	if isChannel(m.Target) {
		room = ellipsize(cleanText(m.Target), maxRoomColumn)
	} else {
		dm = "[dm -> " + cleanText(m.Target) + "]"
	}
	if r.showRoom {
		// Rooms get a padded column so direct messages line up with channel traffic.
		r.roomWidth = max(r.roomWidth, min(utf8.RuneCountInString(room), maxRoomColumn))
	}
	label := "<" + from + ">"
	column := strings.Repeat(" ", r.nickWidth-utf8.RuneCountInString(from)) + label
	styledColumn := strings.Repeat(" ", r.nickWidth-utf8.RuneCountInString(from)) + r.dim("<") + r.bold(r.fg(nickColor(m.From), from)) + r.dim(">")

	prefixWidth := timeColumn + 1 + utf8.RuneCountInString(column) + 1
	prefix := r.header(m.Timestamp) + " "
	if r.showRoom {
		prefixWidth += r.roomWidth + 1
		prefix += r.fg(37, room) + strings.Repeat(" ", max(r.roomWidth-utf8.RuneCountInString(room), 0)) + " "
	}
	prefix += styledColumn + " "
	if dm != "" {
		prefixWidth += utf8.RuneCountInString(dm) + 1
		prefix += r.fg(177, dm) + " "
	}

	if r.width-prefixWidth < compactBelow {
		// Narrow: "12:00 <nick> text", with no padded column and no room tag.
		// Leave room for the text: 9 columns go to "12:00 <" ">" and spaces.
		from = ellipsize(from, max(min(12, r.width-minBodyWidth-9), 3))
		prefixWidth = 5 + 1 + utf8.RuneCountInString(from) + 2 + 1
		prefix = r.dim(m.Timestamp.Local().Format("15:04")) + " " + r.dim("<") + r.bold(r.fg(nickColor(m.From), from)) + r.dim(">") + " "
		if dm != "" {
			room := max(r.width-prefixWidth-minBodyWidth-1, 3)
			dm = "[dm -> " + cleanText(m.Target) + "]"
			if utf8.RuneCountInString(dm) > room {
				dm = ellipsize("->"+cleanText(m.Target), room)
			}
			prefixWidth += utf8.RuneCountInString(dm) + 1
			prefix += r.fg(177, dm) + " "
		}
	}

	lines := r.bodyLines(cleanBody(chatBody(m.ChatMetadata, m.ID, m.From, m.Message)), max(r.width-prefixWidth, 1))
	var out strings.Builder
	rule := r.dayRule(m.Timestamp)
	out.WriteString(rule)
	tall := len(lines) > 1
	if rule == "" && r.afterMessage && (tall || r.prevTall) {
		out.WriteString("\n")
	}
	r.afterMessage, r.prevTall = true, tall
	out.WriteString(prefix + lines[0] + "\n")
	for _, line := range lines[1:] {
		if line == "" {
			out.WriteString("\n")
			continue
		}
		out.WriteString(strings.Repeat(" ", prefixWidth) + line + "\n")
	}
	return out.String()
}

func (r *renderer) notice(at time.Time, marker string, color int, text string) string {
	if at.IsZero() {
		at = time.Now()
	}
	r.afterMessage = false
	column := strings.Repeat(" ", max(r.nickWidth+2-len(marker), 0)) + r.bold(r.fg(color, marker))
	prefixWidth := timeColumn + 1 + max(r.nickWidth+2, len(marker)) + 1
	lead := r.header(at) + " " + column + " "
	if r.width-prefixWidth < compactBelow {
		prefixWidth = len(marker) + 1
		lead = r.bold(r.fg(color, marker)) + " "
	}
	lines := wrapText(text, max(r.width-prefixWidth, minBodyWidth))
	var out strings.Builder
	out.WriteString(r.dayRule(at))
	out.WriteString(lead + r.dim(lines[0]) + "\n")
	for _, line := range lines[1:] {
		out.WriteString(strings.Repeat(" ", prefixWidth) + r.dim(line) + "\n")
	}
	return out.String()
}

func (r *renderer) header(at time.Time) string {
	if at.IsZero() {
		at = time.Now()
	}
	return r.dim(at.Local().Format("15:04:05"))
}

// dayRule prints a separator the first time an event is shown and whenever the
// local date changes.
func (r *renderer) dayRule(at time.Time) string {
	if at.IsZero() {
		at = time.Now()
	}
	day := at.Local().Format("Mon 02 Jan 2006")
	if day == r.lastDay {
		return ""
	}
	first := r.lastDay == ""
	r.lastDay = day
	rule := "--- " + day + " ---"
	if r.color {
		rule = "── " + day + " ──"
	}
	if first {
		return r.dim(rule) + "\n"
	}
	return "\n" + r.dim(rule) + "\n"
}

var (
	inlinePattern    = regexp.MustCompile("(`[^`\n]+`)|(\\*\\*[^*\n]+\\*\\*)|(https?://[^\\s)>\\]]+)|((?:^|[^A-Za-z0-9_@])@[A-Za-z_][A-Za-z0-9_-]*)")
	bulletPattern    = regexp.MustCompile(`^(\s*)[-*] `)
	headingPattern   = regexp.MustCompile(`^(\s*)#{1,6} +(.*)$`)
	addresseePattern = regexp.MustCompile(`^([A-Za-z_\[\]\\^{|}][A-Za-z0-9_\[\]\\^{|}-]{0,29}):( |$)`)
	listItemPattern  = regexp.MustCompile(`^([-*•]|\d+[.)]) `)
)

// markup replaces light markdown (headings, bullets, **bold**, `code`, links)
// with zero-width style marks, one source line at a time. Spans never cross a
// line break in the source, so an unmatched marker is left as literal text.
func markup(body string) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if m := headingPattern.FindStringSubmatch(line); m != nil {
			lines[i] = m[1] + string(markBoldOn) + m[2] + string(markBoldOff)
			continue
		}
		line = bulletPattern.ReplaceAllString(line, "$1• ")
		lines[i] = inlinePattern.ReplaceAllStringFunc(line, func(span string) string {
			switch {
			case strings.HasPrefix(span, "`"):
				return string(markCodeOn) + span[1:len(span)-1] + string(markCodeOff)
			case strings.HasPrefix(span, "**"):
				return string(markBoldOn) + span[2:len(span)-2] + string(markBoldOff)
			case strings.HasPrefix(span, "http"):
				return string(markLinkOn) + span + string(markLinkOff)
			default:
				// An @mention, possibly with the character before it.
				at := strings.IndexByte(span, '@')
				return span[:at] + string(markMentionOn) + string(markColorBase+rune(nickIndex(span[at+1:]))) + span[at:] + string(markMentionOff)
			}
		})
	}
	return strings.Join(lines, "\n")
}

func (r *renderer) markStyle(mark, arg rune) string {
	switch mark {
	case markCodeOn:
		return "38;5;221"
	case markBoldOn:
		return "1"
	case markMentionOn:
		return fmt.Sprintf("1;38;5;%d", nickPalette[int(arg-markColorBase)%len(nickPalette)])
	default:
		return "4;38;5;75"
	}
}

type openStyle struct{ mark, arg rune }

// paint turns style marks into ANSI sequences. A style still open at the end of
// a line is closed there and reopened on the next, so no line leaves the
// terminal in a styled state.
func (r *renderer) paint(lines []string) []string {
	var open []openStyle
	out := make([]string, len(lines))
	for i, line := range lines {
		var b strings.Builder
		for _, style := range open {
			b.WriteString("\x1b[" + r.markStyle(style.mark, style.arg) + "m")
		}
		runes := []rune(line)
		for j := 0; j < len(runes); j++ {
			c := runes[j]
			switch c {
			case markCodeOn, markBoldOn, markLinkOn, markMentionOn:
				style := openStyle{mark: c}
				if c == markMentionOn && j+1 < len(runes) {
					j++
					style.arg = runes[j]
				}
				open = append(open, style)
				b.WriteString("\x1b[" + r.markStyle(style.mark, style.arg) + "m")
			case markCodeOff, markBoldOff, markLinkOff, markMentionOff:
				for k, o := range slices.Backward(open) {
					if o.mark == c-1 {
						open = append(open[:k], open[k+1:]...)
						break
					}
				}
				b.WriteString("\x1b[0m")
				for _, style := range open {
					b.WriteString("\x1b[" + r.markStyle(style.mark, style.arg) + "m")
				}
			default:
				if !isMark(c) {
					b.WriteRune(c)
				}
			}
		}
		if len(open) > 0 {
			b.WriteString("\x1b[0m")
		}
		out[i] = b.String()
	}
	return out
}

// styleAddressee colors a leading "name:" in the named agent's color, so
// "builder: please ..." shows who is being asked.
func (r *renderer) styleAddressee(line string) string {
	m := addresseePattern.FindStringSubmatch(line)
	if m == nil {
		return line
	}
	name := m[1]
	return r.bold(r.fg(nickColor(name), name+":")) + line[len(name)+1:]
}

// cleanText removes control characters and bidirectional overrides so a
// message cannot rewrite the viewer's terminal or visually spoof another line.
func cleanText(text string) string {
	return strings.Map(func(c rune) rune {
		if unicode.IsControl(c) || isMark(c) || (c >= 0x202A && c <= 0x202E) || (c >= 0x2066 && c <= 0x2069) {
			return -1
		}
		return c
	}, text)
}

// cleanBody is cleanText for multi-line text: line breaks stay, tabs become spaces.
func cleanBody(text string) string {
	text = strings.ReplaceAll(text, "\t", "    ")
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = cleanText(line)
	}
	return strings.Join(lines, "\n")
}

// wrapText wraps each line of text to width runes. Leading indentation is kept,
// and wrapped lines of a bullet or numbered item hang under its text.
func wrapText(text string, width int) []string {
	var out []string
	for line := range strings.SplitSeq(text, "\n") {
		trimmed := strings.TrimLeft(line, " ")
		indent := len(line) - len(trimmed)
		if strings.TrimSpace(line) == "" {
			out = append(out, "")
			continue
		}
		hang := indent
		if m := listItemPattern.FindString(trimmed); m != "" {
			hang += utf8.RuneCountInString(m)
		}
		out = append(out, wrapWords(strings.Fields(trimmed), width, indent, hang)...)
	}
	if len(out) == 0 {
		out = []string{""}
	}
	return out
}

func wrapWords(words []string, width, indent, hang int) []string {
	if indent >= width-8 {
		indent, hang = 0, 0
	}
	hang = min(hang, width-8)
	var lines []string
	current := strings.Repeat(" ", indent)
	used := indent
	fresh := true
	flush := func() {
		lines = append(lines, current)
		current, used, fresh = strings.Repeat(" ", hang), hang, true
	}
	for _, word := range words {
		runes := []rune(word)
		if !fresh && used+1+visibleLen(runes) > width {
			flush()
		}
		for fresh && used+visibleLen(runes) > width {
			head, tail := cutVisible(runes, width-used)
			current += string(head)
			runes = tail
			flush()
		}
		if !fresh {
			current += " "
			used++
		}
		current += string(runes)
		used += visibleLen(runes)
		fresh = false
	}
	return append(lines, current)
}

// visibleLen counts the runes that occupy a terminal cell; style marks do not.
func visibleLen(runes []rune) int {
	n := 0
	for _, c := range runes {
		if !isMark(c) {
			n++
		}
	}
	return n
}

// cutVisible splits runes after n visible runes, keeping any marks that follow
// the cut with the tail so style state stays in order.
func cutVisible(runes []rune, n int) (head, tail []rune) {
	seen := 0
	for i, c := range runes {
		if isMark(c) {
			continue
		}
		if seen == n {
			return runes[:i], runes[i:]
		}
		seen++
	}
	return runes, nil
}

// terminalSize returns the columns and rows of the terminal attached to f, or 0, 0.
func terminalSize(f *os.File) (cols, rows int) {
	var size struct{ rows, cols, x, y uint16 }
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&size)))
	if errno != 0 {
		return 0, 0
	}
	return int(size.cols), int(size.rows)
}

// terminalWidth returns the width of the terminal attached to f, or 0.
func terminalWidth(f *os.File) int {
	cols, _ := terminalSize(f)
	return cols
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
