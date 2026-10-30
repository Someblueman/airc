package server

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
)

// A handoff is work passed to one participant, or to anyone in a room. It is
// offered, accepted by one taker (in a room, whoever accepts first), marked
// blocked by its holder while it is stuck and accepted again when it moves,
// and ended with done, which may link the result, or declined or withdrawn.
// Every step is an ordinary message replying to the offer; the state lives
// in an append-only log of its own, so a handoff stays open however long
// history keeps the offer.

const (
	handoffOpen      = "open"
	handoffAccepted  = "accepted"
	handoffBlocked   = "blocked"
	handoffDone      = "done"
	handoffDeclined  = "declined"
	handoffWithdrawn = "withdrawn"
)

// maxLiveHandoffs bounds the open and accepted handoffs kept in memory, so a
// runaway client cannot exhaust it; finished ones are listed for a week. A
// listing answers the newest handoffs it selects, 200 unless it asks for
// up to 1000.
const (
	maxLiveHandoffs  = 10000
	handoffKeep      = 7 * 24 * time.Hour
	maxHandoffRecord = 64 << 10
	handoffPage      = 200
	maxHandoffPage   = 1000
)

// A handoff's record. BlockedReason and BlockedAt are set while it is
// blocked; Link once it is done with a link.
type handoff struct {
	Offer         Message   `json:"offer"`
	State         string    `json:"state"`
	Holder        string    `json:"holder,omitempty"`
	HolderAccount string    `json:"holder_account,omitempty"`
	BlockedReason string    `json:"blocked_reason,omitempty"`
	BlockedAt     time.Time `json:"blocked_at,omitzero"`
	Link          string    `json:"link,omitempty"`
	Updated       time.Time `json:"updated"`
}

func (h handoff) id() string { return h.Offer.ID }
func (h handoff) live() bool {
	return h.State == handoffOpen || h.State == handoffAccepted || h.State == handoffBlocked
}
func (h handoff) offer() Message       { return h.Offer }
func (h handoff) updatedAt() time.Time { return h.Updated }

func validHandoff(h handoff) error {
	m := h.Offer
	if !protocol.ValidMessageID(m.ID) || m.Kind != "handoff" || !protocol.ValidNick(m.From) || !validChannel(m.Target) && !protocol.ValidNick(m.Target) || m.For != "" && !protocol.ValidNick(m.For) || h.Updated.IsZero() {
		return errors.New("invalid saved handoff")
	}
	switch h.State {
	case handoffOpen, handoffDeclined, handoffWithdrawn:
		if h.State == handoffOpen && (h.Holder != "" || h.HolderAccount != "") {
			return errors.New("open handoff with a holder")
		}
	case handoffAccepted, handoffBlocked, handoffDone:
		if !protocol.ValidNick(h.Holder) {
			return errors.New(h.State + " handoff without a holder")
		}
	default:
		return errors.New("invalid handoff state")
	}
	if h.State == handoffBlocked && strings.TrimSpace(h.BlockedReason) == "" || h.State != handoffBlocked && (h.BlockedReason != "" || !h.BlockedAt.IsZero()) {
		return errors.New("blocked reason on a handoff that is not blocked, or missing")
	}
	if h.Link != "" && (h.State != handoffDone || !protocol.ValidLink(h.Link)) {
		return errors.New("invalid handoff link")
	}
	return nil
}

// handoffLedger is the handoffs' ledger: s.handoffs holds the open and
// recent ones and their append-only log.
var handoffLedger = ledger[handoff]{name: "handoffs", valid: validHandoff, maxLive: maxLiveHandoffs, keep: handoffKeep, maxRecord: maxHandoffRecord, event: "handoff_compaction_failed"}

// RestoreHandoffs loads the handoff log at path, keeping every open and
// accepted handoff and finished ones for a week, compacts it and appends
// to it from then on. A torn last line, from a crash mid-write, is dropped.
func (s *Server) RestoreHandoffs(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil || s.handoffs.log != nil {
		return errors.New("configure handoffs once before Serve")
	}
	return s.handoffs.load(path)
}

func handoffEntry(h handoff) protocol.ChatEntry {
	meta := messageMetadata(h.Offer)
	return protocol.ChatEntry{Action: "handoff", Target: h.Offer.Target, ID: h.Offer.ID, From: h.Offer.From, AccountID: h.Offer.AccountID, State: h.State, Holder: h.Holder, Link: h.Link, BlockedReason: h.BlockedReason, BlockedAt: h.BlockedAt, Message: &meta}
}

// isNickLocked reports whether client is nick: by account when the nick is
// registered, as ownership is everywhere else, and by name for guests.
func (s *Server) isNickLocked(client *session, nick string) bool {
	if a, registered := s.accounts[nickKey(nick)]; registered {
		return client.accountID == a.ID
	}
	return client.accountID == "" && strings.EqualFold(client.client.Nick, nick)
}

// authored reports whether client offered m, not merely may act for its author.
func authored(client *session, m Message) bool {
	if m.AccountID != "" {
		return client.accountID == m.AccountID
	}
	return client.accountID == "" && strings.EqualFold(client.client.Nick, m.From)
}

// heldBy reports whether client is the one who accepted h, whatever its
// state now; holdsHandoff, whether it still holds it, accepted or blocked.
func heldBy(client *session, h handoff) bool {
	if h.HolderAccount != "" {
		return client.accountID == h.HolderAccount
	}
	return h.Holder != "" && client.accountID == "" && strings.EqualFold(client.client.Nick, h.Holder)
}

func holdsHandoff(client *session, h handoff) bool {
	return (h.State == handoffAccepted || h.State == handoffBlocked) && heldBy(client, h)
}

// handoffRequest offers a handoff (Text to Target, For a nickname when the
// target is a room), steps handoff ID with an optional note (the reason, on
// blocked) and, on done, a Link to the result, or lists handoffs: one by
// ID, a room's by Target, or up to Limit of the caller's.
type handoffRequest struct {
	Action string
	Target string
	ID     string
	Text   string
	For    string
	Link   string
	Limit  int
}

// decodeHandoffRequest checks the fields of an offer, a listing and the
// steps that carry more than a note. An offer to a nickname is for that
// nickname, so For is resolved here.
func decodeHandoffRequest(_ Config, r protocol.ChatRequest) (handoffRequest, error) {
	request := handoffRequest{Action: r.Action, Target: r.Target, ID: r.ID, Text: r.Text, For: r.For, Link: r.Link, Limit: r.Limit}
	if r.Link != "" && r.Action != "handoff-done" {
		return handoffRequest{}, errors.New("a link goes with done")
	}
	switch r.Action {
	case "handoff-blocked":
		if strings.TrimSpace(r.Text) == "" {
			return handoffRequest{}, errors.New("say why the handoff is blocked")
		}
	case "handoff-done":
		if r.Link != "" && !protocol.ValidLink(r.Link) {
			return handoffRequest{}, fmt.Errorf("a link is an absolute http(s) URL of at most %d bytes", protocol.MaxLinkBytes)
		}
	case "handoffs":
		if r.Target != "" && !validChannel(r.Target) {
			return handoffRequest{}, errors.New("list a room's handoffs by its channel name")
		}
		if r.Limit < 0 || r.Limit > maxHandoffPage {
			return handoffRequest{}, fmt.Errorf("a handoff listing holds at most %d", maxHandoffPage)
		}
	case "handoff":
		if !protocol.IsChannel(r.Target) {
			if r.For != "" && !strings.EqualFold(r.For, r.Target) {
				return handoffRequest{}, errors.New("a direct handoff is for the nickname it is sent to")
			}
			request.For = r.Target
		}
		if request.For != "" && !protocol.ValidNick(request.For) {
			return handoffRequest{}, errors.New("a handoff is for a valid nickname")
		}
	}
	return request, nil
}

// offerHandoffLocked is the table's handler for an offer; the handoff tests
// drive it with the wire request.
func (s *Server) offerHandoffLocked(client *session, r protocol.ChatRequest) error {
	request, err := decodeHandoffRequest(s.cfg, r)
	if err != nil {
		return err
	}
	return s.openHandoffLocked(client, request)
}

func (s *Server) openHandoffLocked(client *session, r handoffRequest) error {
	forNick := r.For
	if forNick != "" && strings.EqualFold(forNick, client.client.Nick) {
		return errors.New("hand off to someone else")
	}
	body, err := s.chatBody(r.Text, "")
	if err != nil {
		return err
	}
	target, err := s.chatTargetLocked(client, r.Target, "handoff")
	if err != nil {
		return err
	}
	if !protocol.IsChannel(target) {
		forNick = target
	}
	if !s.slowAllowedLocked(client, target) {
		return errChatDenied
	}
	if live := liveRecords(s.handoffs.all); live >= maxLiveHandoffs {
		return fmt.Errorf("%d handoffs are open; finish or withdraw some first", live)
	}
	m := s.postMessage(client, target, body, nil)
	m.Kind, m.AccountID, m.For = "handoff", client.accountID, forNick
	h := handoff{Offer: m, State: handoffOpen, Updated: m.Timestamp}
	if err := s.handoffs.save(h); err != nil {
		return err
	}
	s.postHandoffLocked(client, m, h)
	return nil
}

func (s *Server) stepHandoffLocked(client *session, r handoffRequest) error {
	h, found := s.handoffs.all[r.ID]
	if !found {
		return errors.New("handoff is not retained")
	}
	room := protocol.IsChannel(h.Offer.Target)
	author := authored(client, h.Offer)
	// A step repeated after its answer was lost answers again, unposted.
	if r.Action == "handoff-accept" && h.State == handoffAccepted && heldBy(client, h) ||
		r.Action == "handoff-blocked" && h.State == handoffBlocked && heldBy(client, h) ||
		r.Action == "handoff-done" && h.State == handoffDone && heldBy(client, h) ||
		r.Action == "handoff-withdraw" && h.State == handoffWithdrawn && author {
		s.chatEntryLocked(client, handoffEntry(h))
		return nil
	}
	notify := h.Offer.From // who the step is for
	kind := r.Action       // names the step's default text
	detail := ""           // what the step records besides a note
	switch r.Action {
	case "handoff-accept":
		switch {
		case holdsHandoff(client, h):
			// The holder takes a blocked handoff up again.
			kind = "handoff-resume"
		case h.State != handoffOpen:
			return fmt.Errorf("handoff is %s", describeHandoff(h))
		case author:
			return errors.New("you offered this handoff")
		case h.Offer.For != "" && !s.isNickLocked(client, h.Offer.For):
			return errors.New("this handoff is for " + h.Offer.For)
		default:
			h.Holder, h.HolderAccount = client.client.Nick, client.accountID
		}
		h.State, h.BlockedReason, h.BlockedAt = handoffAccepted, "", time.Time{}
	case "handoff-blocked":
		if !h.live() || h.State == handoffOpen {
			return fmt.Errorf("handoff is %s", describeHandoff(h))
		}
		if !holdsHandoff(client, h) && !client.admin {
			return errors.New("only " + h.Holder + ", who holds it, may mark it blocked")
		}
		h.State, h.BlockedReason, h.BlockedAt = handoffBlocked, strings.TrimSpace(protocol.NormalizeNewlines(r.Text)), s.now().UTC()
		detail = h.BlockedReason
	case "handoff-decline":
		switch {
		case holdsHandoff(client, h) && room && h.Offer.For == "":
			// Given back: someone else in the room may take it.
			h.State, h.Holder, h.HolderAccount = handoffOpen, "", ""
		case holdsHandoff(client, h), h.State == handoffOpen && h.Offer.For != "" && s.isNickLocked(client, h.Offer.For):
			h.State = handoffDeclined
		case !h.live():
			return fmt.Errorf("handoff is %s", describeHandoff(h))
		default:
			return errors.New("only the handoff's holder, or the person it is for, may decline it")
		}
		h.BlockedReason, h.BlockedAt = "", time.Time{}
	case "handoff-done":
		if h.State != handoffAccepted && h.State != handoffBlocked {
			return fmt.Errorf("handoff is %s", describeHandoff(h))
		}
		if !holdsHandoff(client, h) && !client.admin {
			return errors.New("only " + h.Holder + ", who accepted it, may mark it done")
		}
		h.State, h.Link, h.BlockedReason, h.BlockedAt = handoffDone, r.Link, "", time.Time{}
		detail = r.Link
	case "handoff-withdraw":
		if !h.live() {
			return fmt.Errorf("handoff is %s", describeHandoff(h))
		}
		if !ownsMessage(client, h.Offer) {
			return errors.New("only the author or an admin may withdraw this handoff")
		}
		notify = cmp.Or(h.Holder, h.Offer.For)
		h.State, h.BlockedReason, h.BlockedAt = handoffWithdrawn, "", time.Time{}
	default:
		return errors.New("unknown CHAT action")
	}
	// A blocked step always names its reason, so a reader that knows no
	// handoffs sees why; every other step is its note, else its default text.
	note := r.Text
	if r.Action == "handoff-blocked" {
		note = ""
	}
	body, err := s.chatBody(note, protocol.HandoffStepText(kind, h.Offer.ID, detail))
	if err != nil {
		return err
	}
	target := h.Offer.Target
	if !room {
		// A direct handoff's steps go to the other side.
		target = h.Offer.From
		if strings.EqualFold(client.client.Nick, h.Offer.From) {
			target = cmp.Or(h.Holder, h.Offer.For)
		}
	}
	if target, err = s.chatTargetLocked(client, target, "handoff"); err != nil {
		return err
	}
	if strings.EqualFold(notify, client.client.Nick) {
		notify = ""
	}
	h.Updated = s.now().UTC()
	m := s.postMessage(client, target, body, &h.Offer)
	m.Kind, m.AccountID, m.For, m.Link = r.Action, client.accountID, notify, r.Link
	if err := s.handoffs.save(h); err != nil {
		return err
	}
	s.postHandoffLocked(client, m, h)
	return nil
}

// postHandoffLocked posts a step's message and answers with the state the
// step produced: a later step may land while this one waits for its sync.
func (s *Server) postHandoffLocked(client *session, m Message, h handoff) {
	s.postStructuredLocked(client, m)
	s.chatEntryLocked(client, handoffEntry(h))
}

func describeHandoff(h handoff) string {
	switch h.State {
	case handoffAccepted:
		return "already accepted by " + h.Holder
	case handoffBlocked:
		return "blocked by " + h.Holder + ": " + h.BlockedReason
	case handoffDone:
		return "already done by " + h.Holder
	case handoffOpen:
		return "still open"
	}
	return "already " + h.State
}

// listHandoffsLocked answers one handoff by id, every retained handoff in a
// room, or, by default, the live handoffs that concern client: for it, held
// by it, offered by it, or open to anyone in a room it is in, the blocked
// ones first since they need someone. A one-shot agent joins no room, so the
// rooms it checked, waited or posted in within the activity window count as
// its rooms.
func (s *Server) listHandoffsLocked(client *session, r handoffRequest) error {
	limit := cmp.Or(r.Limit, handoffPage)
	now := s.now()
	_, recent, _ := s.recentLocked(client.client.Nick, now)
	var out []handoff
	for _, h := range sortedRecords(s.handoffs.all) {
		if !h.live() && now.Sub(h.Updated) > handoffKeep {
			continue
		}
		switch {
		case r.ID != "":
			if h.Offer.ID != r.ID {
				continue
			}
		case r.Target != "":
			if h.Offer.Target != r.Target {
				continue
			}
		default:
			if !h.live() {
				continue
			}
			_, joined := client.channels[h.Offer.Target]
			joined = joined || slices.Contains(recent, h.Offer.Target)
			mine := h.Offer.For != "" && s.isNickLocked(client, h.Offer.For) || holdsHandoff(client, h) || authored(client, h.Offer) ||
				h.State == handoffOpen && h.Offer.For == "" && joined
			if !mine {
				continue
			}
		}
		out = append(out, h)
	}
	if r.ID != "" && len(out) == 0 {
		return errors.New("handoff is not retained")
	}
	out = out[max(0, len(out)-limit):]
	if r.ID == "" && r.Target == "" {
		slices.SortStableFunc(out, func(a, b handoff) int {
			switch blocked := func(h handoff) bool { return h.State == handoffBlocked }; {
			case blocked(a) && !blocked(b):
				return -1
			case blocked(b) && !blocked(a):
				return 1
			}
			return 0
		})
	}
	for _, h := range out {
		s.chatEntryLocked(client, handoffEntry(h))
	}
	return nil
}
