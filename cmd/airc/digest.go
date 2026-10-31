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

// digestHandoff is an open, accepted or blocked handoff that concerns the
// digest's nick, whether or not anything about it is unread. A blocked one
// says why and since when.
type digestHandoff struct {
	ID            string    `json:"id"`
	State         string    `json:"state"`
	From          string    `json:"from"`
	Target        string    `json:"target"`
	For           string    `json:"for,omitempty"`
	Holder        string    `json:"holder,omitempty"`
	BlockedReason string    `json:"blocked_reason,omitempty"`
	BlockedAt     time.Time `json:"blocked_at,omitzero"`
	Preview       string    `json:"preview"`
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
	// Resolved says a resolution closed the thread: its root needs no answer.
	Resolved bool `json:"resolved,omitempty"`
}

// digestStatus is a nickname's latest status post in a room among what was
// read: where everyone is, without their narration.
type digestStatus struct {
	Nick  string    `json:"nick"`
	State string    `json:"state"`
	Task  string    `json:"task"`
	Link  string    `json:"link,omitempty"`
	At    time.Time `json:"at"`
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
	Statuses []digestStatus  `json:"statuses,omitempty"` // the latest per nickname, newest first
}

// digestPending is a send of the digest's nick whose outcome is unknown: its
// outbox entry has no receipt. send --pending lists them; send --retry
// recovers a receipt without posting twice.
type digestPending struct {
	RequestID string    `json:"request_id"`
	Target    string    `json:"target"`
	Created   time.Time `json:"created,omitzero"`
	Preview   string    `json:"preview"`
}

// digestGoal is an orchestrated room among the digest's rooms: who
// orchestrates it and toward what goal. For the room's orchestrator it
// counts the room's handoffs by state, done ones from the last week.
type digestGoal struct {
	Target        string `json:"target"`
	Orchestrator  string `json:"orchestrator"`
	Goal          string `json:"goal,omitempty"`
	Orchestrating bool   `json:"orchestrating,omitempty"`
	Open          int    `json:"open,omitempty"`
	InProgress    int    `json:"in_progress,omitempty"`
	Blocked       int    `json:"blocked,omitempty"`
	Done          int    `json:"done,omitempty"`
}

type digestHeader struct {
	Type    string `json:"type"` // topic, charter or pin
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
	Goals         []digestGoal     `json:"goals,omitempty"`
	Waiting       []digestItem     `json:"waiting_on_you"`
	WaitingMore   int              `json:"waiting_omitted,omitempty"`
	Corrections   []digestItem     `json:"corrections,omitempty"`
	Polls         []digestItem     `json:"open_polls,omitempty"`
	Handoffs      []digestHandoff  `json:"handoffs,omitempty"`
	HandoffsMore  int              `json:"handoffs_omitted,omitempty"`
	Decisions     []digestDecision `json:"decisions,omitempty"`
	DecisionsMore int              `json:"decisions_omitted,omitempty"`
	Headers       []digestHeader   `json:"headers,omitempty"`
	PendingSends  []digestPending  `json:"pending_sends,omitempty"`
	PendingMore   int              `json:"pending_sends_omitted,omitempty"`
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
	summary.Goals = orchestratedRooms(ctx, *opt, settings.channels, &summary.Warnings)
	summary.PendingSends = pendingSends(*opt, &summary.Warnings)
	if len(summary.PendingSends) > digestWaiting {
		summary.PendingMore = len(summary.PendingSends) - digestWaiting
		summary.PendingSends = summary.PendingSends[:digestWaiting]
	}
	summary.MarkedRead = *markRead
	if opt.json {
		return json.NewEncoder(stdout).Encode(summary)
	}
	return writeDigest(stdout, summary, time.Now())
}

// pendingSends lists this nick's sends to this endpoint whose outcome is
// unknown, from the outbox send --pending reads, so an agent catching up
// learns of them here rather than by knowing to ask. The digest reads the
// outbox as it reads everything: it changes nothing. An unreadable outbox is
// a warning, since the rest of the digest stands.
func pendingSends(opt options, warnings *[]string) []digestPending {
	entries, err := pendingOutbox(opt)
	if err != nil {
		*warnings = append(*warnings, "could not read the send outbox, so uncertain sends may be missing: "+err.Error())
		return nil
	}
	var out []digestPending
	for _, e := range entries {
		out = append(out, digestPending{RequestID: e.RequestID, Target: e.Target, Created: e.Created, Preview: preview(e.Body)})
	}
	return out
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
	// A thread with a resolution is settled, whoever posted it: its root
	// carries the mark while the resolution is retained, and the resolution
	// itself names the root.
	resolved := map[string]bool{}
	for _, m := range rows.messages {
		if m.Resolved {
			resolved[m.ID] = true
		}
		if m.Kind == "resolved" && m.ThreadID != "" {
			resolved[m.ThreadID] = true
		}
	}
	type roomState struct {
		room     digestRoom
		speakers map[string]*digestSpeaker
		threads  map[string]*digestThread
		order    []string // thread keys in first-seen order
		statuses map[string]*digestStatus
	}
	rooms := map[string]*roomState{}
	var roomOrder []string
	// Where everyone is: the latest status per nickname in each room, own
	// ones included, since they count toward nothing else.
	statuses := map[string]map[string]*digestStatus{}
	for _, m := range rows.messages {
		if m.Kind != "status" || !protocol.IsChannel(m.Target) {
			continue
		}
		if statuses[m.Target] == nil {
			statuses[m.Target] = map[string]*digestStatus{}
		}
		if s := statuses[m.Target][strings.ToLower(m.From)]; s == nil || m.Timestamp.After(s.At) {
			statuses[m.Target][strings.ToLower(m.From)] = &digestStatus{Nick: m.From, State: m.State, Task: m.Task, Link: m.Link, At: m.Timestamp}
		}
	}
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
			r = &roomState{room: digestRoom{Target: name, FirstID: m.ID, FirstAt: m.Timestamp}, speakers: map[string]*digestSpeaker{}, threads: map[string]*digestThread{}, statuses: statuses[name]}
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
			t.Resolved = t.Resolved || resolved[thread]
			if !slices.ContainsFunc(t.Participants, func(p string) bool { return strings.EqualFold(p, m.From) }) {
				t.Participants = append(t.Participants, m.From)
			}
		}
		item := digestItem{ID: m.ID, From: m.From, Target: m.Target, At: m.Timestamp, Preview: preview(m.Message), ReplyTo: m.ReplyTo}
		if m.Mentioned {
			r.room.Mentions++
			d.Addressed++
			// Handoffs and decisions are listed by state, and their steps
			// need no reply; a status is narration, a resolution closes a
			// thread, and a settled thread waits on nobody.
			settled := resolved[m.ID] || m.ThreadID != "" && resolved[m.ThreadID]
			if !repliedTo[m.ID] && !settled && !strings.HasPrefix(m.Kind, "handoff") && !strings.HasPrefix(m.Kind, "decision") && m.Kind != "status" && m.Kind != "resolved" && m.Kind != "orchestrate" {
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
		for _, s := range r.statuses {
			r.room.Statuses = append(r.room.Statuses, *s)
		}
		slices.SortFunc(r.room.Statuses, func(a, b digestStatus) int {
			return cmp.Or(b.At.Compare(a.At), cmp.Compare(strings.ToLower(a.Nick), strings.ToLower(b.Nick)))
		})
		if len(r.room.Statuses) > digestSpeakers {
			r.room.Statuses = r.room.Statuses[:digestSpeakers]
		}
		d.Rooms = append(d.Rooms, r.room)
	}
	for _, h := range rows.headers {
		if len(d.Headers) == digestNotes {
			break
		}
		d.Headers = append(d.Headers, digestHeader{Type: h.Type, Target: h.Target, ID: h.ID, From: h.From, Preview: preview(cmp.Or(h.Topic, h.Charter))})
	}
	return d
}

// digestReplyChecks bounds the reply lookups one digest makes.
const digestReplyChecks = 40

// resolveAnswered drops the messages this nick has replied to with a reply
// link the rows did not show: replies to direct messages, which live in the
// sender's conversation rather than this inbox, and replies in rooms the
// digest did not read. A resolution by anyone settles the message too. A
// lookup that fails keeps the message, with a warning.
func resolveAnswered(ctx context.Context, opt options, waiting []digestItem, warnings *[]string) []digestItem {
	if len(waiting) == 0 {
		return waiting
	}
	checked := min(len(waiting), digestReplyChecks)
	answered := map[string]bool{}
	err := chatRequestWithContext(ctx, opt, func(ctx context.Context, client *irc.Client) error {
		for _, item := range waiting[:checked] {
			page, err := fetchHistory(ctx, client, "replies:"+item.ID, "", 100, nil)
			if err != nil {
				return err
			}
			for _, reply := range page.messages {
				if reply.Kind == "resolved" || strings.EqualFold(reply.From, opt.nick) && reply.Reaction == "" {
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

// openHandoffs lists the live handoffs that concern opt.nick, the blocked
// ones first as the daemon orders them, then those open to anyone in the
// first digestRooms of rooms: a one-shot command joins no room, so the
// daemon cannot know them. A failure warns.
func openHandoffs(ctx context.Context, opt options, rooms []string, warnings *[]string) []digestHandoff {
	var out []digestHandoff
	seen := map[string]bool{}
	err := chatRequestWithContext(ctx, opt, func(ctx context.Context, client *irc.Client) error {
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
				h := digestHandoff{ID: e.ID, State: e.State, From: e.From, Target: e.Target, Holder: e.Holder, BlockedReason: e.BlockedReason, BlockedAt: e.BlockedAt}
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

// orchestratedRooms reads the orchestrator mode of the first digestRooms of
// rooms, and for each room opt.nick orchestrates, the room's handoffs by
// state. A failure warns.
func orchestratedRooms(ctx context.Context, opt options, rooms []string, warnings *[]string) []digestGoal {
	var out []digestGoal
	err := chatRequestWithContext(ctx, opt, func(ctx context.Context, client *irc.Client) error {
		for _, room := range rooms[:min(len(rooms), digestRooms)] {
			if room = channelName(room); !protocol.IsChannel(room) {
				continue
			}
			entries, err := irc.RequestChat(ctx, client, irc.ChatRequest{Action: "room", Target: room, Seconds: -1, Limit: -1}, nil)
			if err != nil {
				return err
			}
			for _, e := range entries {
				if e.Orchestrator == "" {
					continue
				}
				g := digestGoal{Target: room, Orchestrator: e.Orchestrator, Goal: e.Goal, Orchestrating: strings.EqualFold(e.Orchestrator, opt.nick)}
				if g.Orchestrating {
					handoffs, err := irc.RequestChat(ctx, client, irc.ChatRequest{Action: "handoffs", Target: room}, nil)
					if err != nil {
						return err
					}
					for _, h := range handoffs {
						switch h.State {
						case "open":
							g.Open++
						case "accepted":
							g.InProgress++
						case "blocked":
							g.Blocked++
						case "done":
							g.Done++
						}
					}
				}
				out = append(out, g)
			}
		}
		return nil
	})
	if err != nil {
		*warnings = append(*warnings, "could not read the rooms' orchestrators: "+err.Error())
	}
	return out
}

// openDecisions lists the open decisions that concern opt.nick: for it to
// make or asked by it, and those asked in the first digestRooms of rooms,
// since a one-shot command joins no room. Each carries whether its asker is
// a human, from the card the daemon attaches. A failure warns.
func openDecisions(ctx context.Context, opt options, rooms []string, warnings *[]string) []digestDecision {
	var out []digestDecision
	seen := map[string]bool{}
	err := chatRequestWithContext(ctx, opt, func(ctx context.Context, client *irc.Client) error {
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
	if d.Unread == 0 && len(d.Headers) == 0 && len(d.Gaps) == 0 && len(d.Handoffs) == 0 && len(d.Decisions) == 0 && len(d.PendingSends) == 0 {
		return nil
	}
	var b strings.Builder
	atLeast := ""
	if d.More {
		atLeast = "at least "
	}
	fmt.Fprintf(&b, "airc digest for %s: %s%d unread in %d %s, %d addressed to you (%d waiting on you)\n", d.Nick, atLeast, d.Unread, len(d.Rooms), plural(len(d.Rooms), "place", "places"), d.Addressed, len(d.Waiting)+d.WaitingMore)
	// Before anything else: a send with no receipt is settled by recovering
	// the receipt, never by posting again.
	if n := len(d.PendingSends) + d.PendingMore; n > 0 {
		fmt.Fprintf(&b, "%d uncertain %s; airc send --pending lists %s; --retry REQUEST_ID recovers a receipt\n", n, plural(n, "send", "sends"), plural(n, "it", "them"))
	}
	for _, g := range d.Goals {
		b.WriteString(goalLine(g) + "\n")
	}
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
			mark := ""
			if t.Resolved {
				mark = " (resolved)"
			}
			fmt.Fprintf(&b, "  thread %s: %d messages from %s, starting %q%s\n", t.ID, t.Messages, strings.Join(t.Participants, ", "), t.Preview, mark)
		}
	}
	writeStatuses(&b, d, now)
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
	writeHandoffs(&b, d)
	writeDecisions(&b, d)
	for _, h := range d.Headers {
		switch h.Type {
		case "pin":
			fmt.Fprintf(&b, "Pinned in %s [%s] by %s: %q\n", h.Target, h.ID, h.From, h.Preview)
		case "charter":
			fmt.Fprintf(&b, "Charter of %s changed: %q\n", h.Target, h.Preview)
		default:
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

// goalLine is an orchestrated room's line in the digest: its goal, and for
// its orchestrator, its handoffs by state.
//
//	Orchestrating #work: ship the parser fix; 2 open · 1 in progress · 0 blocked · 3 done this week
//	Goal of #work (lead orchestrates): ship the parser fix
func goalLine(g digestGoal) string {
	goal := cmp.Or(g.Goal, "no goal set")
	if g.Orchestrating {
		return fmt.Sprintf("Orchestrating %s: %s; %d open · %d in progress · %d blocked · %d done this week", g.Target, goal, g.Open, g.InProgress, g.Blocked, g.Done)
	}
	return fmt.Sprintf("Goal of %s (%s orchestrates): %s", g.Target, g.Orchestrator, goal)
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

// writeStatuses says where everyone is: the latest status per nickname in
// each summarised room, newest first:
//
//	Where everyone is:
//	  #work builder [done] port the fix (12:01) https://github.com/o/r/pull/54
//	  #work reviewer [working] review the parser (11:58)
func writeStatuses(b *strings.Builder, d digestSummary, now time.Time) {
	wrote := false
	for _, r := range d.Rooms {
		for _, s := range r.Statuses {
			if !wrote {
				b.WriteString("Where everyone is:\n")
				wrote = true
			}
			fmt.Fprintf(b, "  %s %s [%s] %s (%s)", r.Target, s.Nick, s.State, s.Task, clock(s.At, now))
			if s.Link != "" {
				b.WriteString(" " + s.Link)
			}
			b.WriteString("\n")
		}
	}
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// writeHandoffs lists the handoffs that concern nick, the blocked ones first
// since they need someone: those nick handed over and the holder marked
// blocked, with the reason, then those nick holds and marked blocked, then
// the rest:
//
//	Blocked handoffs you handed over:
//	  [ID] you handed over to builder, blocked by builder in #work: "finish task 7" blocked: waiting on CI
//	Handoffs you marked blocked:
//	  [ID] from planner, blocked by you in #work: "port the fix" blocked: needs a credential
//	Handoffs:
//	  [ID] from planner for you, open in #work: "triage the flaky test"
func writeHandoffs(b *strings.Builder, d digestSummary) {
	if len(d.Handoffs) == 0 {
		return
	}
	var handedOver, held, rest []digestHandoff
	for _, h := range d.Handoffs {
		switch {
		case h.State != "blocked":
			rest = append(rest, h)
		case strings.EqualFold(h.From, d.Nick):
			handedOver = append(handedOver, h)
		default:
			held = append(held, h)
		}
	}
	blockedLine := func(h digestHandoff) string {
		return fmt.Sprintf("  [%s] %s in %s: %q blocked: %s\n", h.ID, handoffStanding(h, d.Nick), h.Target, h.Preview, h.BlockedReason)
	}
	if len(handedOver) > 0 {
		b.WriteString("Blocked handoffs you handed over:\n")
		for _, h := range handedOver {
			b.WriteString(blockedLine(h))
		}
	}
	if len(held) > 0 {
		b.WriteString("Handoffs you marked blocked:\n")
		for _, h := range held {
			b.WriteString(blockedLine(h))
		}
	}
	if len(rest) > 0 {
		b.WriteString("Handoffs:\n")
		for _, h := range rest {
			fmt.Fprintf(b, "  [%s] %s in %s: %q\n", h.ID, handoffStanding(h, d.Nick), h.Target, h.Preview)
		}
	}
	if d.HandoffsMore > 0 {
		fmt.Fprintf(b, "  and %d more; airc handoffs lists them\n", d.HandoffsMore)
	}
}

// handoffStanding says where a handoff stands for nick: who it is from or
// for, and who holds it or marked it blocked.
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
	switch h.State {
	case "accepted":
		return text + ", accepted by " + who(h.Holder)
	case "blocked":
		return text + ", blocked by " + who(h.Holder)
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
