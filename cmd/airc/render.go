package main

import (
	"fmt"
	"hash/fnv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// nickPalette holds 256-color codes that stay readable on both dark and light
// terminals. A nickname always maps to the same entry.
var nickPalette = []int{38, 208, 41, 170, 141, 178, 43, 204, 75, 112, 177, 209, 71, 135, 172, 37}

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

func (r *renderer) dim(text string) string { return r.style(text, "2") }

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
	if protocol.IsChannel(m.Target) {
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

	lines := r.bodyLines(cleanBody(chatBody(m.ChatMetadata, m.ID, m.ReplyTo, m.From, m.Message)), max(r.width-prefixWidth, 1))
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

// chatBody renders a message body with the markers its chat metadata calls
// for. replyTo names the handoff a handoff step belongs to.
func chatBody(m irc.ChatMetadata, id, replyTo, from, text string) string {
	body := text
	switch m.Kind {
	case "notice":
		body = "[notice] " + body
	case "bot":
		body = "[bot] " + body
	case "action":
		body = "* " + from + " " + body
	case "correct":
		body = "[correction of " + m.Supersedes + "] " + body
	case "retract":
		body = "[retraction of " + m.Supersedes + "] " + body
	case "poll":
		body = "[poll " + id + "] " + body
		for i, option := range m.PollOptions {
			body += fmt.Sprintf("\n%d. %s", i+1, option)
		}
	case "handoff":
		if m.For != "" {
			body = "[handoff " + id + " for " + m.For + "] " + body
		} else {
			body = "[handoff " + id + "] " + body
		}
	case "handoff-accept", "handoff-decline", "handoff-done", "handoff-withdraw":
		// A step without a note already says what it did.
		if label := protocol.HandoffStepText(m.Kind, replyTo); body != label {
			body = "[" + strings.ToLower(label[:1]) + label[1:] + "] " + body
		}
	}
	if m.Retracted {
		body = "[retracted; see " + m.SupersededBy + "] " + body
	} else if m.SupersededBy != "" {
		body = "[superseded; see " + m.SupersededBy + "] " + body
	}
	return strings.TrimRight(body, "\n")
}
