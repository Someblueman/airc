package main

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Someblueman/airc/pkg/irc"
)

// uiCommand is one slash command of the full-screen client: how to type it,
// what it does, and the daemon capability it needs. The table drives Tab
// completion and the /help view, so a command is documented wherever it is
// added.
type uiCommand struct {
	name  string
	usage string // arguments after the name; "ID" first means a message ID
	help  string
	needs string // a 005 capability, or "" for commands every daemon serves
	group string
}

var uiCommands = []uiCommand{
	{"/help", "", "this list, with what the connected daemon supports", "", "General"},
	{"/join", "#channel", "open a channel and follow it live", "", "General"},
	{"/msg", "nick text", "send a direct message", "", "General"},
	{"/topic", "[text|-]", "show, set or clear the channel header", "TOPIC", "General"},
	{"/charter", "", "open this room's charter, its longer rules, in a view; /close returns", "CHAT", "General"},
	{"/run", "ID", "a read-only view of this room's messages posted under run ID, oldest first", "RUN", "General"},
	{"/search", "words", "search this room: every word must occur, \"phrases\" stay together, -word excludes", "SEARCH", "General"},
	{"/close", "", "close this room, thread, search or help view", "", "General"},
	{"/quit", "", "leave the UI (also Ctrl-C)", "", "General"},
	{"/context", "ID", "retained context, pins and corrections, with live replies", "CONTEXT", "Conversations"},
	{"/thread", "ID", "open a conversation with live replies; typing replies to its latest message", "REPLIES", "Conversations"},
	{"/reply", "ID text", "reply to a particular message", "REPLIES", "Conversations"},
	{"/react", "ID symbol", "react to a message (never approval)", "REACTIONS", "Conversations"},
	{"/follow", "ID", "add a thread to this user's CLI checks", "REPLIES", "Conversations"},
	{"/unfollow", "ROOT_ID", "remove a followed thread", "REPLIES", "Conversations"},
	{"/next", "", "continue an open thread or search", "REPLIES", "Conversations"},
	{"/first", "", "restart a thread or search from the oldest retained results", "REPLIES", "Conversations"},
	{"/paste", "ID", "open a shared paste read-only, 200 lines a page; /next continues", "PASTE", "Conversations"},
	{"/pin", "ID", "share a message as a room pin", "CHAT", "Room"},
	{"/unpin", "ID", "remove a room pin", "CHAT", "Room"},
	{"/pins", "", "list this room's pins", "CHAT", "Room"},
	{"/correct", "ID text", "publish a correction to your message", "CHAT", "Room"},
	{"/retract", "ID reason", "retract your message", "CHAT", "Room"},
	{"/prepare", "ID note", "promise a reply within two minutes", "CHAT", "Room"},
	{"/waiting", "ID", "who has promised a reply", "CHAT", "Room"},
	{"/cancel", "ID", "cancel your own promise", "CHAT", "Room"},
	{"/me", "text", "an action in this room", "CHAT", "Room"},
	{"/poll", "question | a | b", "a one-hour poll in this room", "CHAT", "Room"},
	{"/vote", "ID number", "vote in a poll", "CHAT", "Room"},
	{"/results", "ID", "poll results", "CHAT", "Room"},
	{"/close-poll", "ID", "close your poll", "CHAT", "Room"},
	{"/status", "[task | working|blocked|done | note]", "post where you are in this room; alone, list the room's latest statuses", "CHAT", "Room"},
	{"/resolve", "ID [note]", "close a question settled elsewhere: its author, who it addresses, or an admin", "CHAT", "Conversations"},
	{"/handoff", "[@nick] text", "hand work to this room (first to accept takes it), to nick, or in a DM to them", "HANDOFF", "Handoffs"},
	{"/accept", "ID [note]", "take a handoff", "HANDOFF", "Handoffs"},
	{"/done", "ID [note]", "tell everyone a handoff you hold is finished", "HANDOFF", "Handoffs"},
	{"/decline", "ID [note]", "turn a handoff down, or give one you hold back to the room", "HANDOFF", "Handoffs"},
	{"/blocked", "ID reason", "say a handoff you hold is stuck; /accept takes it up again", "HANDOFF", "Handoffs"},
	{"/withdraw", "ID [note]", "call off your handoff", "HANDOFF", "Handoffs"},
	{"/handoffs", "", "handoffs for you, held by you or handed over by you", "HANDOFF", "Handoffs"},
	{"/board", "[#room]", "a room's handoffs by state: open, in progress, blocked and done this week; live steps redraw it", "HANDOFF", "Handoffs"},
	{"/decide", "@nick text | option | option", "ask nick (in a DM, the other person) to decide; options after | are numbered", "DECISION", "Decisions"},
	{"/answer", "ID [N] [note]", "answer a decision asked of you: the option number, a note, or both", "DECISION", "Decisions"},
	{"/withdraw-decision", "ID [note]", "call off your decision", "DECISION", "Decisions"},
	{"/decisions", "", "decisions waiting on you and those you asked", "DECISION", "Decisions"},
	{"/away", "[reason]", "publish one-hour away presence, or clear it", "", "Presence and operators"},
	{"/op", "nick", "grant channel operator with your account", "CHANNEL_OPERATORS", "Presence and operators"},
	{"/deop", "nick", "revoke channel operator", "CHANNEL_OPERATORS", "Presence and operators"},
	{"/kick", "nick [reason]", "remove someone from this channel", "CHANNEL_OPERATORS", "Presence and operators"},
	{"/mute", "nick [reason]", "mute everywhere, with the default admin credential", "ADMIN", "Moderation"},
	{"/unmute", "nick", "lift a mute", "ADMIN", "Moderation"},
	{"/ban", "nick [reason]", "ban everywhere", "ADMIN", "Moderation"},
	{"/unban", "nick", "lift a ban", "ADMIN", "Moderation"},
	{"/disconnect", "nick [reason]", "disconnect every session of a nickname", "ADMIN", "Moderation"},
	{"/bans", "", "active mutes and bans", "ADMIN", "Moderation"},
}

// helpLine is one row of the /help view: a heading alone, or a key or command
// and what it does, laid out in two columns when the terminal is wide enough.
type helpLine struct {
	head, body string
}

var uiKeys = []helpLine{
	{"Tab", "complete a nick, #channel or /command; Tab again cycles the matches, Shift-Tab cycles back"},
	{"Tab, Shift-Tab", "with nothing typed: the next or previous view (Ctrl-N and Ctrl-P always)"},
	{"Shift-Up/Down", "select a message (Option or Ctrl with Up/Down work too)"},
	{"Ctrl-O", "open the selected message's context"},
	{"Ctrl-R", "reply to the selected message; the status bar shows what you are answering"},
	{"Ctrl-T", "open the selected message's thread"},
	{"Ctrl-Y", "insert the selected message's ID at the cursor, and offer it to the clipboard"},
	{"Up, Down", "recall a line sent from this view when nothing of yours is typed; otherwise scroll one line"},
	{"PgUp, PgDn", "scroll"},
	{"Esc", "clear the draft and the reply target"},
	{"Ctrl-A/E/U/K/W", "line editing: start, end, cut to the start, cut to the end, cut the word"},
	{"Ctrl-C", "quit (Ctrl-D with nothing typed)"},
}

// supports reports a daemon capability, true until the daemon has said
// otherwise so nothing is refused before the first connection.
func (m *uiModel) supports(feature string) bool {
	if feature == "" || m.features == nil {
		return true
	}
	value, ok := m.features[feature]
	return ok && value != "0"
}

// helpLines is the content of the /help view for the connected daemon.
func (m *uiModel) helpLines() []helpLine {
	lines := []helpLine{{head: "Keys"}}
	lines = append(lines, uiKeys...)
	lines = append(lines, helpLine{body: "Commands take `last` in place of an ID for the newest message in the view."})
	lines = append(lines, helpLine{body: "Members: ☺ marks a human, ● a connected session. A registered author's <nick> is bold, a guest's dim; humans are blue."})
	if m.features == nil {
		lines = append(lines, helpLine{body: "Not connected yet: which commands the daemon supports is unknown."})
	}
	group := ""
	for _, c := range uiCommands {
		if c.group != group {
			group = c.group
			lines = append(lines, helpLine{head: group})
		}
		line := helpLine{head: strings.TrimSpace(c.name + " " + c.usage), body: c.help}
		if !m.supports(c.needs) {
			line.body += " [unavailable: the daemon lacks " + c.needs + "; upgrade and restart it when safe]"
		}
		lines = append(lines, line)
	}
	return lines
}

// helpText is the help view as plain lines, for tests and narrow terminals.
func helpText(lines []helpLine) string {
	var out []string
	for _, l := range lines {
		out = append(out, strings.TrimSpace(l.head+"  "+l.body))
	}
	return strings.Join(out, "\n")
}

// renderHelp lays the help out in width columns: headings in bold, then a
// column of keys and commands with their descriptions wrapped beside them, or
// under them when the terminal is narrow.
func renderHelp(lines []helpLine, width int) []string {
	column := 0
	for _, l := range lines {
		if l.body != "" {
			column = max(column, utf8.RuneCountInString(l.head))
		}
	}
	column = min(column+2, width/2)
	var out []string
	for _, l := range lines {
		switch {
		case l.body == "":
			out = append(out, "", sty("1", fitPlain(l.head, width)))
		case l.head == "":
			out = append(out, wrapText(l.body, width)...)
		case utf8.RuneCountInString(l.head)+2 > column: // too wide for the column
			out = append(out, sty("1", fitPlain(l.head, width)))
			for _, body := range wrapText(l.body, max(width-4, 10)) {
				out = append(out, "    "+body)
			}
		default:
			for i, body := range wrapText(l.body, max(width-column, 10)) {
				lead := strings.Repeat(" ", column)
				if i == 0 {
					lead = sty("1", l.head) + strings.Repeat(" ", column-utf8.RuneCountInString(l.head))
				}
				out = append(out, lead+body)
			}
		}
	}
	if len(out) > 0 && out[0] == "" {
		out = out[1:]
	}
	return out
}

func (m *uiModel) showHelp() {
	b := m.find("help")
	if b == nil {
		b = newBuffer("help", bufHelp)
		b.returnTo = m.current
		m.buffers = append(m.buffers, b)
	}
	b.kind = bufHelp
	b.help = m.helpLines()
	b.topic = "Keys and commands · /close returns to " + b.returnTo
	b.version++
	m.switchTo("help")
}

// completion is a Tab completion in progress: the input span it replaces and
// the candidates it cycles through. Any other key ends it.
type completion struct {
	start, end int
	candidates []string
	index      int
}

// completeWord completes the word before the cursor, or cycles to the next
// candidate when the previous key was also Tab. It reports whether the key was
// consumed; with nothing typed, Tab is left to switch views.
func (m *uiModel) completeWord(delta int) bool {
	if len(m.input) == 0 {
		return false
	}
	c := &m.complete
	if len(c.candidates) == 0 {
		start := m.cursor
		for start > 0 && m.input[start-1] != ' ' && m.input[start-1] != '\n' {
			start--
		}
		prefix := string(m.input[start:m.cursor])
		candidates := m.completions(start, prefix)
		if len(candidates) == 0 {
			m.setStatus("Nothing completes "+strings.TrimSpace(prefix)+" here (Ctrl-N and Ctrl-P switch views)", true)
			return true
		}
		*c = completion{start: start, end: m.cursor, candidates: candidates, index: -1}
	}
	c.index = (c.index + delta + len(c.candidates)) % len(c.candidates)
	text := []rune(c.candidates[c.index])
	m.input = append(append(append([]rune{}, m.input[:c.start]...), text...), m.input[c.end:]...)
	c.end = c.start + len(text)
	m.cursor = c.end
	if len(c.candidates) > 1 {
		m.setStatus(fmt.Sprintf("%d of %d: Tab for the next", c.index+1, len(c.candidates)), false)
	} else {
		*c = completion{} // nothing to cycle through; the next Tab completes the next word
	}
	return true
}

// completions lists what the word starting at start, typed so far as prefix,
// can become: a command at the start of the line, a channel after #, a message
// ID after a command that takes one, otherwise a nickname. Each candidate
// carries the separator that follows it.
func (m *uiModel) completions(start int, prefix string) []string {
	var out []string
	lower := strings.ToLower(prefix)
	switch {
	case start == 0 && strings.HasPrefix(prefix, "/"):
		for _, c := range uiCommands {
			if strings.HasPrefix(c.name, lower) {
				out = append(out, c.name+" ")
			}
		}
	case strings.HasPrefix(prefix, "#"):
		for _, b := range m.buffers {
			if b.kind == bufChannel && strings.HasPrefix(strings.ToLower(b.name), lower) {
				out = append(out, b.name+" ")
			}
		}
	case m.wantsID(start):
		if b := m.cur(); b != nil && b.selectedID != "" && strings.HasPrefix(b.selectedID, prefix) {
			out = append(out, b.selectedID+" ")
		}
		if strings.HasPrefix("last", lower) {
			out = append(out, "last ")
		}
	default:
		at := strings.HasPrefix(prefix, "@")
		name := strings.TrimPrefix(lower, "@")
		for _, nick := range m.completionNicks() {
			if !strings.HasPrefix(strings.ToLower(nick), name) {
				continue
			}
			switch {
			case at:
				out = append(out, "@"+nick+" ")
			case start == 0:
				out = append(out, nick+": ")
			default:
				out = append(out, nick+" ")
			}
		}
	}
	return out
}

// wantsID reports whether the word at start is the first argument of a
// command that takes a message ID.
func (m *uiModel) wantsID(start int) bool {
	fields := strings.Fields(string(m.input[:start]))
	if len(fields) != 1 || !strings.HasPrefix(string(m.input), "/") {
		return false
	}
	for _, c := range uiCommands {
		if c.name == strings.ToLower(fields[0]) {
			return strings.HasPrefix(c.usage, "ID") || strings.HasPrefix(c.usage, "ROOT_ID")
		}
	}
	return false
}

// completionNicks is everyone worth addressing, most likely first: who spoke
// in this view, newest first; then the sessions connected to it; then
// speakers elsewhere. One's own nick is left out.
func (m *uiModel) completionNicks() []string {
	var out []string
	seen := map[string]bool{strings.ToLower(m.nick): true}
	add := func(nick string) {
		key := strings.ToLower(nick)
		if nick != "" && !seen[key] {
			seen[key] = true
			out = append(out, nick)
		}
	}
	speakers := func(b *uiBuffer) {
		for i := len(b.items) - 1; i >= 0; i-- {
			if message, ok := b.items[i].(*irc.MessageEvent); ok {
				add(message.From)
			}
		}
	}
	current := m.cur()
	if current != nil {
		speakers(current)
		live := append([]string{}, current.live...)
		sort.Strings(live)
		for _, nick := range live {
			add(nick)
		}
	}
	for _, b := range m.buffers {
		if b != current {
			speakers(b)
		}
	}
	return out
}

// messagePreview is who wrote a retained message and how it starts, for the
// status bar; "" when no open view holds it.
func (m *uiModel) messagePreview(id string) string {
	for _, b := range m.buffers {
		for _, item := range b.items {
			if e, ok := item.(*irc.MessageEvent); ok && e.ID == id {
				return ellipsize(e.From+": "+cleanText(strings.ReplaceAll(e.Message, "\n", " ")), 60)
			}
		}
	}
	return ""
}

// threadReplyTarget is the message a plain line in a thread view answers: the
// newest retained message, else the thread root.
func threadReplyTarget(b *uiBuffer) string {
	if b == nil || b.kind != bufQuery || !strings.HasPrefix(b.name, "thread:") {
		return ""
	}
	for i := len(b.items) - 1; i >= 0; i-- {
		if last, ok := b.items[i].(*irc.MessageEvent); ok && last.ID != "" {
			return last.ID
		}
	}
	return strings.TrimPrefix(b.name, "thread:")
}
