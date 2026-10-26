package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// The digest's bounds: what one call returns stays small whatever the backlog.
const (
	digestPreview  = 160 // runes of a message shown
	digestSpeakers = 5   // per room
	digestThreads  = 3   // per room, busiest first
	digestWaiting  = 20  // unanswered messages addressed to you
	digestNotes    = 10  // corrections, polls, headers and pins each
	digestRooms    = 8   // rooms whose open handoffs are listed
)

// digestItem is one message, cut to a preview.
type digestItem struct {
	ID         string    `json:"id"`
	From       string    `json:"from"`
	Target     string    `json:"target"`
	At         time.Time `json:"at"`
	Preview    string    `json:"preview"`
	ReplyTo    string    `json:"reply_to,omitempty"`
	Supersedes string    `json:"supersedes,omitempty"`
	Kind       string    `json:"kind,omitempty"`
	Options    []string  `json:"options,omitempty"`
	ClosesAt   time.Time `json:"closes_at,omitzero"`
}

// digestHandoff is an open or accepted handoff that concerns the digest's
// nick, whether or not anything about it is unread.
type digestHandoff struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	From    string `json:"from"`
	Target  string `json:"target"`
	For     string `json:"for,omitempty"`
	Holder  string `json:"holder,omitempty"`
	Preview string `json:"preview"`
}

// digestDecision is an open decision that concerns the digest's nick: one
// for it to make, or one it asked. Human says the asker is marked as a
// person in the directory, so a decider knows someone is waiting.
type digestDecision struct {
	ID      string   `json:"id"`
	State   string   `json:"state"`
	From    string   `json:"from"`
	Target  string   `json:"target"`
	Decider string   `json:"decider"`
	Options []string `json:"options,omitempty"`
	Preview string   `json:"preview"`
	Human   bool     `json:"human,omitempty"`
}

type digestSpeaker struct {
	Nick     string `json:"nick"`
	Messages int    `json:"messages"`
}

type digestThread struct {
	ID           string   `json:"id"` // the thread root
	Messages     int      `json:"messages"`
	Participants []string `json:"participants"`
	FirstID      string   `json:"first_id"`
	Preview      string   `json:"preview"` // the first unread message in it
}

type digestRoom struct {
	Target   string          `json:"target"` // a room, or "inbox" for direct messages
	Unread   int             `json:"unread"`
	Mentions int             `json:"mentions,omitempty"`
	FirstID  string          `json:"first_id"`
	LastID   string          `json:"last_id"`
	FirstAt  time.Time       `json:"first_at"`
	LastAt   time.Time       `json:"last_at"`
	Speakers []digestSpeaker `json:"speakers"`
	Others   int             `json:"other_speakers,omitempty"`
	Threads  []digestThread  `json:"threads,omitempty"`
}

type digestHeader struct {
	Type    string `json:"type"` // topic or pin
	Target  string `json:"target"`
	ID      string `json:"id,omitempty"`
	From    string `json:"from,omitempty"`
	Preview string `json:"preview"`
}

// digestSummary describes unread traffic without its bodies: who said how
// much where, what is addressed to you and still unanswered, and what
// changed the record (corrections, polls, headers, pins).
type digestSummary struct {
	Type          string           `json:"type"`
	Nick          string           `json:"nick"`
	Unread        int              `json:"unread"`
	Addressed     int              `json:"addressed"`
	More          bool             `json:"more"`
	MarkedRead    bool             `json:"marked_read"`
	Rooms         []digestRoom     `json:"rooms"`
	Waiting       []digestItem     `json:"waiting_on_you"`
	WaitingMore   int              `json:"waiting_omitted,omitempty"`
	Corrections   []digestItem     `json:"corrections,omitempty"`
	Polls         []digestItem     `json:"open_polls,omitempty"`
	Handoffs      []digestHandoff  `json:"handoffs,omitempty"`
	HandoffsMore  int              `json:"handoffs_omitted,omitempty"`
	Decisions     []digestDecision `json:"decisions,omitempty"`
	DecisionsMore int              `json:"decisions_omitted,omitempty"`
	Headers       []digestHeader   `json:"headers,omitempty"`
	Gaps          []string         `json:"gaps,omitempty"`
	Warnings      []string         `json:"warnings,omitempty"`
}

func runDigestSession(ctx context.Context, session *agentConnection, args []string, stdout, stderr io.Writer) (resultErr error) {
	fs := flag.NewFlagSet("airc digest", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	opt.session = session
	settings := &checkOptions{peek: true, includeOwn: true, limit: 100, initial: 20, maxMessages: 1000, maxBytes: 1 << 20}
	fs.Var(&settings.channels, "channel", "room to summarize besides the inbox; repeat or comma-separate (env AIRC_CHANNEL)")
	markRead := fs.Bool("mark-read", false, "mark everything summarized as read, as check would; read what matters first with context or thread")
	defer func() { reportFailure(&resultErr, opt.json, stderr) }()
	if err := parseAgentFlags(fs, args, opt, stderr); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: airc digest [--nick NAME] [--channel ROOM]... [--mark-read] [--json]")
	}
	if err := identity(opt); err != nil {
		return err
	}
	settings.peek = !*markRead
	var rows checkRows
	// A peek reads the same first page every time, so it reads one; marking
	// read moves on, so it reads until the backlog is summarized.
	for page := 0; page < maxCheckPages; page++ {
		next, err := readCheck(ctx, *opt, settings)
		if err != nil {
			return err
		}
		rows.messages = append(rows.messages, next.messages...)
		rows.headers = append(rows.headers, next.headers...)
		rows.status.Gaps = append(rows.status.Gaps, next.status.Gaps...)
		rows.status.Warnings = append(rows.status.Warnings, next.status.Warnings...)
		rows.status.More = next.status.More
		if settings.peek || !next.status.More {
			break
		}
	}
	summary := summarize(opt.nick, rows, time.Now())
	summary.Waiting = resolveAnswered(ctx, *opt, summary.Waiting, &summary.Warnings)
	if len(summary.Waiting) > digestWaiting {
		summary.WaitingMore = len(summary.Waiting) - digestWaiting
		summary.Waiting = summary.Waiting[:digestWaiting]
	}
	summary.Handoffs = openHandoffs(ctx, *opt, settings.channels, &summary.Warnings)
	if len(summary.Handoffs) > digestWaiting {
		summary.HandoffsMore = len(summary.Handoffs) - digestWaiting
		summary.Handoffs = summary.Handoffs[:digestWaiting]
	}
	summary.Decisions = openDecisions(ctx, *opt, settings.channels, &summary.Warnings)
	if len(summary.Decisions) > digestWaiting {
		summary.DecisionsMore = len(summary.Decisions) - digestWaiting
		summary.Decisions = summary.Decisions[:digestWaiting]
	}
	summary.MarkedRead = *markRead
	if opt.json {
		return json.NewEncoder(stdout).Encode(summary)
	}
	return writeDigest(stdout, summary, time.Now())
}

// summarize builds a digest from check rows, own messages included: they
// count toward nothing, but show which addressed messages were answered.
func summarize(nick string, rows checkRows, now time.Time) digestSummary {
	d := digestSummary{Type: "digest", Nick: nick, More: rows.status.More, Rooms: []digestRoom{}, Waiting: []digestItem{}, Gaps: unique(rows.status.Gaps), Warnings: unique(rows.status.Warnings)}
	own := func(m checkMessage) bool { return strings.EqualFold(m.From, nick) }
	// What this nick answered with a reply link, among what was read.
	// Replies carry their thread's root ID; a root carries none, so find
	// the roots to count each with its replies. Corrections and reactions
	// are linked too, but are not conversation.
	conversational := func(m checkMessage) bool { return m.Supersedes == "" && m.Reaction == "" }
	repliedTo, roots := map[string]bool{}, map[string]bool{}
	for _, m := range rows.messages {
		if own(m) && m.ReplyTo != "" && m.Reaction == "" {
			repliedTo[m.ReplyTo] = true
		}
		if m.ThreadID != "" && conversational(m) {
			roots[m.ThreadID] = true
		}
	}
	type roomState struct {
		room     digestRoom
		speakers map[string]*digestSpeaker
		threads  map[string]*digestThread
		order    []string // thread keys in first-seen order
	}
	rooms := map[string]*roomState{}
	var roomOrder []string
	for _, m := range rows.messages {
		if own(m) {
			continue
		}
		name := "inbox"
		if protocol.IsChannel(m.Target) {
			name = m.Target
		}
		r := rooms[name]
		if r == nil {
			r = &roomState{room: digestRoom{Target: name, FirstID: m.ID, FirstAt: m.Timestamp}, speakers: map[string]*digestSpeaker{}, threads: map[string]*digestThread{}}
			rooms[name] = r
			roomOrder = append(roomOrder, name)
		}
		r.room.Unread++
		r.room.LastID, r.room.LastAt = m.ID, m.Timestamp
		d.Unread++
		speaker := r.speakers[strings.ToLower(m.From)]
		if speaker == nil {
			speaker = &digestSpeaker{Nick: m.From}
			r.speakers[strings.ToLower(m.From)] = speaker
		}
		speaker.Messages++
		thread := m.ThreadID
		if thread == "" && roots[m.ID] {
			thread = m.ID
		}
		if thread != "" && conversational(m) {
			t := r.threads[thread]
			if t == nil {
				t = &digestThread{ID: thread, FirstID: m.ID, Preview: preview(m.Message)}
				r.threads[thread] = t
				r.order = append(r.order, thread)
			}
			t.Messages++
			if !slices.ContainsFunc(t.Participants, func(p string) bool { return strings.EqualFold(p, m.From) }) {
				t.Participants = append(t.Participants, m.From)
			}
		}
		item := digestItem{ID: m.ID, From: m.From, Target: m.Target, At: m.Timestamp, Preview: preview(m.Message), ReplyTo: m.ReplyTo}
		if m.Mentioned {
			r.room.Mentions++
			d.Addressed++
			// Handoffs and decisions are listed by state, and their steps
			// need no reply.
			if !repliedTo[m.ID] && !strings.HasPrefix(m.Kind, "handoff") && !strings.HasPrefix(m.Kind, "decision") {
				d.Waiting = append(d.Waiting, item) // resolveAnswered asks the server about the rest
			}
		}
		switch {
		case m.Supersedes != "" && len(d.Corrections) < digestNotes:
			item.Kind, item.Supersedes = m.Kind, m.Supersedes
			d.Corrections = append(d.Corrections, item)
		case m.Kind == "poll" && m.PollClosesAt.After(now) && len(d.Polls) < digestNotes:
			item.Options, item.ClosesAt = m.PollOptions, m.PollClosesAt
			d.Polls = append(d.Polls, item)
		}
	}
	for _, name := range roomOrder {
		r := rooms[name]
		for _, s := range r.speakers {
			r.room.Speakers = append(r.room.Speakers, *s)
		}
		slices.SortFunc(r.room.Speakers, func(a, b digestSpeaker) int {
			return cmp.Or(cmp.Compare(b.Messages, a.Messages), cmp.Compare(strings.ToLower(a.Nick), strings.ToLower(b.Nick)))
		})
		if len(r.room.Speakers) > digestSpeakers {
			r.room.Others = len(r.room.Speakers) - digestSpeakers
			r.room.Speakers = r.room.Speakers[:digestSpeakers]
		}
		for _, key := range r.order {
			if t := r.threads[key]; t.Messages >= 2 {
				r.room.Threads = append(r.room.Threads, *t)
			}
		}
		slices.SortStableFunc(r.room.Threads, func(a, b digestThread) int { return cmp.Compare(b.Messages, a.Messages) })
		if len(r.room.Threads) > digestThreads {
			r.room.Threads = r.room.Threads[:digestThreads]
		}
		d.Rooms = append(d.Rooms, r.room)
	}
	for _, h := range rows.headers {
		if len(d.Headers) == digestNotes {
			break
		}
		d.Headers = append(d.Headers, digestHeader{Type: h.Type, Target: h.Target, ID: h.ID, From: h.From, Preview: preview(h.Topic)})
	}
	return d
}

// digestReplyChecks bounds the reply lookups one digest makes.
const digestReplyChecks = 40

// resolveAnswered drops the messages this nick has replied to with a reply
// link the rows did not show: replies to direct messages, which live in the
// sender's conversation rather than this inbox, and replies in rooms the
// digest did not read. A lookup that fails keeps the message, with a warning.
func resolveAnswered(ctx context.Context, opt options, waiting []digestItem, warnings *[]string) []digestItem {
	if len(waiting) == 0 {
		return waiting
	}
	checked := min(len(waiting), digestReplyChecks)
	answered := map[string]bool{}
	err := chatRequestWithContext(ctx, opt, "REPLIES", func(ctx context.Context, client *irc.Client) error {
		for _, item := range waiting[:checked] {
			page, err := fetchHistory(ctx, client, "replies:"+item.ID, "", 100, nil)
			if err != nil {
				return err
			}
			for _, reply := range page.messages {
				if strings.EqualFold(reply.From, opt.nick) && reply.Reaction == "" {
					answered[item.ID] = true
				}
			}
		}
		return nil
	})
	if err != nil {
		*warnings = append(*warnings, "could not look up your replies, so some listed messages may already be answered: "+err.Error())
	}
	return slices.DeleteFunc(waiting, func(item digestItem) bool { return answered[item.ID] })
}

// openHandoffs lists the open and accepted handoffs that concern opt.nick,
// then those open to anyone in the first digestRooms of rooms: a one-shot
// command joins no room, so the daemon cannot know them. A daemon without
// handoffs has none; other failures warn.
func openHandoffs(ctx context.Context, opt options, rooms []string, warnings *[]string) []digestHandoff {
	var out []digestHandoff
	seen := map[string]bool{}
	err := chatRequestWithContext(ctx, opt, "CHAT", func(ctx context.Context, client *irc.Client) error {
		if !client.Supports("HANDOFF") {
			return nil
		}
		requests := []irc.ChatRequest{{Action: "handoffs"}}
		for _, room := range rooms[:min(len(rooms), digestRooms)] {
			if room = channelName(room); protocol.IsChannel(room) {
				requests = append(requests, irc.ChatRequest{Action: "handoffs", Target: room})
			}
		}
		for i, r := range requests {
			entries, err := irc.RequestChat(ctx, client, r, nil)
			if err != nil {
				return err
			}
			for _, e := range entries {
				anyone := e.State == "open" && (e.Message == nil || e.Message.For == "") && !strings.EqualFold(e.From, opt.nick)
				if seen[e.ID] || i > 0 && !anyone {
					continue
				}
				seen[e.ID] = true
				h := digestHandoff{ID: e.ID, State: e.State, From: e.From, Target: e.Target, Holder: e.Holder}
				if e.Message != nil {
					h.For, h.Preview = e.Message.For, preview(e.Message.Message)
				}
				out = append(out, h)
			}
		}
		return nil
	})
	if err != nil {
		*warnings = append(*warnings, "could not list your handoffs: "+err.Error())
	}
	return out
}

// openDecisions lists the open decisions that concern opt.nick: for it to
// make or asked by it, and those asked in the first digestRooms of rooms,
// since a one-shot command joins no room. Each carries whether its asker is
// a human, from the card the daemon attaches. A daemon without decisions
// has none; other failures warn.
func openDecisions(ctx context.Context, opt options, rooms []string, warnings *[]string) []digestDecision {
	var out []digestDecision
	seen := map[string]bool{}
	err := chatRequestWithContext(ctx, opt, "CHAT", func(ctx context.Context, client *irc.Client) error {
		if !client.Supports("DECISION") {
			return nil
		}
		requests := []irc.ChatRequest{{Action: "decisions"}}
		for _, room := range rooms[:min(len(rooms), digestRooms)] {
			if room = channelName(room); protocol.IsChannel(room) {
				requests = append(requests, irc.ChatRequest{Action: "decisions", Target: room})
			}
		}
		for _, r := range requests {
			entries, err := irc.RequestChat(ctx, client, r, nil)
			if err != nil {
				return err
			}
			for _, e := range entries {
				if seen[e.ID] || e.State != "open" {
					continue
				}
				seen[e.ID] = true
				d := digestDecision{ID: e.ID, State: e.State, From: e.From, Target: e.Target}
				if e.Message != nil {
					d.Decider, d.Options, d.Preview = e.Message.For, e.Message.Choices, preview(e.Message.Message)
				}
				if e.Profile != nil {
					d.Human = e.Profile.Kind == "human"
				}
				out = append(out, d)
			}
		}
		return nil
	})
	if err != nil {
		*warnings = append(*warnings, "could not list your decisions: "+err.Error())
	}
	return out
}

// preview is a message on one line, cut to digestPreview runes.
func preview(text string) string {
	return ellipsize(strings.Join(strings.Fields(cleanText(strings.NewReplacer("\n", " ", "\t", " ").Replace(text))), " "), digestPreview)
}

func unique(values []string) []string {
	var out []string
	for _, v := range values {
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// writeDigest prints a digest for a person or an agent without JSON. Like
// unread, it prints nothing when there is nothing new, so a hook adds nothing.
func writeDigest(out io.Writer, d digestSummary, now time.Time) error {
	if d.Unread == 0 && len(d.Headers) == 0 && len(d.Gaps) == 0 && len(d.Handoffs) == 0 && len(d.Decisions) == 0 {
		return nil
	}
	var b strings.Builder
	atLeast := ""
	if d.More {
		atLeast = "at least "
	}
	fmt.Fprintf(&b, "airc digest for %s: %s%d unread in %d %s, %d addressed to you (%d waiting on you)\n", d.Nick, atLeast, d.Unread, len(d.Rooms), plural(len(d.Rooms), "place", "places"), d.Addressed, len(d.Waiting)+d.WaitingMore)
	for _, r := range d.Rooms {
		var people []string
		for _, s := range r.Speakers {
			people = append(people, fmt.Sprintf("%s %d", s.Nick, s.Messages))
		}
		if r.Others > 0 {
			people = append(people, fmt.Sprintf("+%d more", r.Others))
		}
		tags := ""
		if r.Mentions > 0 {
			tags = fmt.Sprintf(", %d for you", r.Mentions)
		}
		when := clock(r.FirstAt, now) + " to " + clock(r.LastAt, now)
		if first, last := clock(r.FirstAt, now), clock(r.LastAt, now); first == last {
			when = "at " + first
		}
		fmt.Fprintf(&b, "%s: %d unread%s, %s, from %s\n", r.Target, r.Unread, tags, when, strings.Join(people, ", "))
		for _, t := range r.Threads {
			fmt.Fprintf(&b, "  thread %s: %d messages from %s, starting %q\n", t.ID, t.Messages, strings.Join(t.Participants, ", "), t.Preview)
		}
	}
	section := func(title string, items []digestItem, line func(digestItem) string) {
		if len(items) == 0 {
			return
		}
		b.WriteString(title + ":\n")
		for _, item := range items {
			b.WriteString("  " + line(item) + "\n")
		}
	}
	section("Waiting on you", d.Waiting, func(i digestItem) string {
		where := i.Target
		if !protocol.IsChannel(where) {
			where = "DM"
		}
		return fmt.Sprintf("[%s] %s %s %s: %q", i.ID, clock(i.At, now), where, i.From, i.Preview)
	})
	if d.WaitingMore > 0 {
		fmt.Fprintf(&b, "  and %d more\n", d.WaitingMore)
	}
	section("Corrections", d.Corrections, func(i digestItem) string {
		verb := "corrected"
		if i.Kind == "retract" {
			verb = "retracted"
		}
		return fmt.Sprintf("[%s] %s %s %s: %q", i.ID, i.From, verb, i.Supersedes, i.Preview)
	})
	section("Open polls", d.Polls, func(i digestItem) string {
		return fmt.Sprintf("[%s] %s %s: %q (%s), closes %s", i.ID, i.Target, i.From, i.Preview, strings.Join(i.Options, " / "), clock(i.ClosesAt, now))
	})
	if len(d.Handoffs) > 0 {
		b.WriteString("Handoffs:\n")
		for _, h := range d.Handoffs {
			fmt.Fprintf(&b, "  [%s] %s in %s: %q\n", h.ID, handoffStanding(h, d.Nick), h.Target, h.Preview)
		}
		if d.HandoffsMore > 0 {
			fmt.Fprintf(&b, "  and %d more; airc handoffs lists them\n", d.HandoffsMore)
		}
	}
	writeDecisions(&b, d)
	for _, h := range d.Headers {
		if h.Type == "pin" {
			fmt.Fprintf(&b, "Pinned in %s [%s] by %s: %q\n", h.Target, h.ID, h.From, h.Preview)
		} else {
			fmt.Fprintf(&b, "Header of %s: %q\n", h.Target, h.Preview)
		}
	}
	for _, gap := range d.Gaps {
		fmt.Fprintf(&b, "Gap: unread messages in %s expired before they were read\n", gap)
	}
	for _, warning := range d.Warnings {
		fmt.Fprintf(&b, "Warning: %s\n", warning)
	}
	switch {
	case d.MarkedRead && d.More:
		b.WriteString("Marked what was summarized as read; more remains, so run airc digest --mark-read again.\n")
	case d.MarkedRead:
		b.WriteString("Marked as read. Read a message in full with airc context ID.\n")
	default:
		b.WriteString("Nothing was marked read. Read in full with airc check, or one message with airc context ID; airc digest --mark-read skips the rest.\n")
	}
	_, err := io.WriteString(out, b.String())
	return err
}

// writeDecisions lists the decisions waiting on nick, humans' first, then
// the ones nick asked and others open in its rooms:
//
//	Decisions waiting on you:
//	  [ID] a human (sws) asks in #work: "Ship with the simple parser?" options: yes / no
//	Your open decisions:
//	  [ID] you asked sws in #work: "Rename the flag?"
func writeDecisions(b *strings.Builder, d digestSummary) {
	if len(d.Decisions) == 0 {
		return
	}
	var waiting, others []digestDecision
	for _, item := range d.Decisions {
		if strings.EqualFold(item.Decider, d.Nick) {
			waiting = append(waiting, item)
		} else {
			others = append(others, item)
		}
	}
	slices.SortStableFunc(waiting, func(a, b digestDecision) int { return cmp.Compare(boolInt(b.Human), boolInt(a.Human)) })
	line := func(item digestDecision, who string) string {
		text := fmt.Sprintf("  [%s] %s in %s: %q", item.ID, who, item.Target, item.Preview)
		if len(item.Options) > 0 {
			text += " options: " + strings.Join(item.Options, " / ")
		}
		return text + "\n"
	}
	if len(waiting) > 0 {
		b.WriteString("Decisions waiting on you:\n")
		for _, item := range waiting {
			who := item.From + " asks"
			if item.Human {
				who = "a human (" + item.From + ") asks"
			}
			b.WriteString(line(item, who))
		}
	}
	if len(others) > 0 {
		b.WriteString("Your open decisions:\n")
		for _, item := range others {
			who := item.From + " asked " + item.Decider
			if strings.EqualFold(item.From, d.Nick) {
				who = "you asked " + item.Decider
			}
			b.WriteString(line(item, who))
		}
	}
	if d.DecisionsMore > 0 {
		fmt.Fprintf(b, "  and %d more; airc decisions lists them\n", d.DecisionsMore)
	}
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// handoffStanding says where a handoff stands for nick: who it is from or
// for, and who holds it.
func handoffStanding(h digestHandoff, nick string) string {
	who := func(name string) string {
		if strings.EqualFold(name, nick) {
			return "you"
		}
		return name
	}
	text := "from " + who(h.From)
	if strings.EqualFold(h.From, nick) {
		text = "you handed over"
		if h.For != "" {
			text += " to " + h.For
		}
	} else if h.For != "" {
		text += " for " + who(h.For)
	}
	if h.State == "accepted" {
		return text + ", accepted by " + who(h.Holder)
	}
	return text + ", open"
}

// clock is a time as a person reads it: the time today, else the date too.
func clock(t, now time.Time) string {
	t = t.Local()
	if y, m, d := t.Date(); y == now.Year() && m == now.Month() && d == now.Day() {
		return t.Format("15:04")
	}
	return t.Format("Jan 2 15:04")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
