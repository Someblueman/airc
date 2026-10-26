package server

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/protocol"
)

// A handoff is work passed to one participant, or to anyone in a room. It is
// offered, accepted by one taker (in a room, whoever accepts first) and ended
// with done, or declined or withdrawn. Every step is an ordinary message
// replying to the offer; the state lives in an append-only log of its own,
// so a handoff stays open however long history keeps the offer.

const (
	handoffOpen      = "open"
	handoffAccepted  = "accepted"
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

type handoff struct {
	Offer         Message   `json:"offer"`
	State         string    `json:"state"`
	Holder        string    `json:"holder,omitempty"`
	HolderAccount string    `json:"holder_account,omitempty"`
	Updated       time.Time `json:"updated"`
}

func (h handoff) live() bool { return h.State == handoffOpen || h.State == handoffAccepted }

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
	case handoffAccepted, handoffDone:
		if !protocol.ValidNick(h.Holder) {
			return errors.New("accepted handoff without a holder")
		}
	default:
		return errors.New("invalid handoff state")
	}
	return nil
}

// RestoreHandoffs loads the handoff log at path, keeping every open and
// accepted handoff and finished ones for a week, compacts it and appends
// to it from then on. A torn last line, from a crash mid-write, is dropped.
func (s *Server) RestoreHandoffs(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil || s.handoffLog != nil {
		return errors.New("configure handoffs once before Serve")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	loaded, err := readHandoffs(path)
	if err != nil {
		return fmt.Errorf("handoffs %s: %w", path, err)
	}
	if liveHandoffs(loaded) > maxLiveHandoffs {
		return fmt.Errorf("handoffs %s: more than %d open", path, maxLiveHandoffs)
	}
	s.handoffs = loaded
	s.handoffLog = &appendLog[handoff]{path: path}
	return s.compactHandoffsLocked()
}

func readHandoffs(path string) (map[string]handoff, error) {
	return readLog(path, maxHandoffRecord, func(h handoff) (string, error) { return h.Offer.ID, validHandoff(h) })
}

// compactHandoffsLocked drops handoffs finished more than a week ago and
// rewrites the log with one line per handoff, then appends to the new file.
func (s *Server) compactHandoffsLocked() error {
	now := s.now()
	for id, h := range s.handoffs {
		if !h.live() && now.Sub(h.Updated) > handoffKeep {
			delete(s.handoffs, id)
		}
	}
	return s.handoffLog.rewrite(sortedHandoffs(s.handoffs), s.cfg.Sync)
}

// saveHandoffLocked records h's new state before it takes effect, and
// compacts the log once superseded lines outnumber the handoffs held.
func (s *Server) saveHandoffLocked(h handoff) error {
	if l := s.handoffLog; l != nil {
		if l.dirty {
			if err := s.compactHandoffsLocked(); err != nil {
				return err
			}
		}
		if err := l.append(h, s.cfg.Sync); err != nil {
			return err
		}
	}
	s.handoffs[h.Offer.ID] = h
	if l := s.handoffLog; l != nil && l.crowded(len(s.handoffs)) {
		if err := s.compactHandoffsLocked(); err != nil {
			s.logger.Warn("handoff_compaction_failed", "error", err)
		}
	}
	return nil
}

func liveHandoffs(all map[string]handoff) int {
	n := 0
	for _, h := range all {
		if h.live() {
			n++
		}
	}
	return n
}

func sortedHandoffs(all map[string]handoff) []handoff {
	out := make([]handoff, 0, len(all))
	for _, h := range all {
		out = append(out, h)
	}
	slices.SortFunc(out, func(a, b handoff) int {
		return cmp.Or(a.Offer.Timestamp.Compare(b.Offer.Timestamp), cmp.Compare(a.Offer.Seq, b.Offer.Seq), cmp.Compare(a.Offer.ID, b.Offer.ID))
	})
	return out
}

func handoffEntry(h handoff) protocol.ChatEntry {
	meta := messageMetadata(h.Offer)
	return protocol.ChatEntry{Action: "handoff", Target: h.Offer.Target, ID: h.Offer.ID, From: h.Offer.From, AccountID: h.Offer.AccountID, State: h.State, Holder: h.Holder, Message: &meta}
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
// state now; holdsHandoff, whether it still holds it.
func heldBy(client *session, h handoff) bool {
	if h.HolderAccount != "" {
		return client.accountID == h.HolderAccount
	}
	return h.Holder != "" && client.accountID == "" && strings.EqualFold(client.client.Nick, h.Holder)
}

func holdsHandoff(client *session, h handoff) bool {
	return h.State == handoffAccepted && heldBy(client, h)
}

func (s *Server) handoffLocked(client *session, r protocol.ChatRequest) error {
	switch r.Action {
	case "handoffs":
		return s.listHandoffsLocked(client, r)
	case "handoff":
		return s.offerHandoffLocked(client, r)
	}
	return s.stepHandoffLocked(client, r)
}

func (s *Server) handoffBody(text, fallback string) (string, error) {
	body := protocol.NormalizeNewlines(text)
	if strings.TrimSpace(body) == "" {
		body = fallback
	}
	if strings.TrimSpace(body) == "" || len(body) > s.cfg.MaxMessageSize || !utf8.ValidString(body) || strings.ContainsRune(body, 0) {
		return "", errors.New("message must be nonempty valid UTF-8 within the message limit")
	}
	return body, nil
}

// handoffTargetLocked checks that client may post a handoff step to target
// and returns the target as the step's message names it.
func (s *Server) handoffTargetLocked(client *session, target string) (string, error) {
	if !validChannel(target) && !protocol.ValidNick(target) {
		return "", errors.New("a handoff needs a room or a nickname")
	}
	if !s.postAllowedLocked(client, target) {
		return "", errChatDenied
	}
	if protocol.IsChannel(target) {
		if _, joined := client.channels[target]; !joined && !client.ephemeral {
			return "", errors.New("join the room before posting")
		}
		return target, nil
	}
	if recipient := s.liveNickLocked(target); recipient != nil && !recipient.observer {
		return recipient.client.Nick, nil
	}
	if s.cfg.HistoryLimit == 0 {
		return "", errors.New("offline direct messages require history")
	}
	return target, nil
}

func (s *Server) offerHandoffLocked(client *session, r protocol.ChatRequest) error {
	forNick := r.For
	if !protocol.IsChannel(r.Target) {
		if forNick != "" && !strings.EqualFold(forNick, r.Target) {
			return errors.New("a direct handoff is for the nickname it is sent to")
		}
		forNick = r.Target
	}
	if forNick != "" && !protocol.ValidNick(forNick) {
		return errors.New("a handoff is for a valid nickname")
	}
	if forNick != "" && strings.EqualFold(forNick, client.client.Nick) {
		return errors.New("hand off to someone else")
	}
	body, err := s.handoffBody(r.Text, "")
	if err != nil {
		return err
	}
	target, err := s.handoffTargetLocked(client, r.Target)
	if err != nil {
		return err
	}
	if !protocol.IsChannel(target) {
		forNick = target
	}
	if !s.slowAllowedLocked(client, target) {
		return errChatDenied
	}
	if live := liveHandoffs(s.handoffs); live >= maxLiveHandoffs {
		return fmt.Errorf("%d handoffs are open; finish or withdraw some first", live)
	}
	m := s.newMessage(client.client.Nick, target, body, nil)
	m.Kind, m.AccountID, m.For = "handoff", client.accountID, forNick
	h := handoff{Offer: m, State: handoffOpen, Updated: m.Timestamp}
	if err := s.saveHandoffLocked(h); err != nil {
		return err
	}
	s.postHandoffLocked(client, m, h)
	return nil
}

func (s *Server) stepHandoffLocked(client *session, r protocol.ChatRequest) error {
	h, found := s.handoffs[r.ID]
	if !found {
		return errors.New("handoff is not retained")
	}
	room := protocol.IsChannel(h.Offer.Target)
	author := authored(client, h.Offer)
	// A step repeated after its answer was lost answers again, unposted.
	if r.Action == "handoff-accept" && holdsHandoff(client, h) ||
		r.Action == "handoff-done" && h.State == handoffDone && heldBy(client, h) ||
		r.Action == "handoff-withdraw" && h.State == handoffWithdrawn && author {
		s.chatEntryLocked(client, handoffEntry(h))
		return nil
	}
	notify := h.Offer.From // who the step is for
	switch r.Action {
	case "handoff-accept":
		switch {
		case h.State != handoffOpen:
			return fmt.Errorf("handoff is %s", describeHandoff(h))
		case author:
			return errors.New("you offered this handoff")
		case h.Offer.For != "" && !s.isNickLocked(client, h.Offer.For):
			return errors.New("this handoff is for " + h.Offer.For)
		}
		h.State, h.Holder, h.HolderAccount = handoffAccepted, client.client.Nick, client.accountID
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
	case "handoff-done":
		if h.State != handoffAccepted {
			return fmt.Errorf("handoff is %s", describeHandoff(h))
		}
		if !holdsHandoff(client, h) && !client.admin {
			return errors.New("only " + h.Holder + ", who accepted it, may mark it done")
		}
		h.State = handoffDone
	case "handoff-withdraw":
		if !h.live() {
			return fmt.Errorf("handoff is %s", describeHandoff(h))
		}
		if !ownsMessage(client, h.Offer) {
			return errors.New("only the author or an admin may withdraw this handoff")
		}
		notify = cmp.Or(h.Holder, h.Offer.For)
		h.State = handoffWithdrawn
	default:
		return errors.New("unknown CHAT action")
	}
	body, err := s.handoffBody(r.Text, protocol.HandoffStepText(r.Action, h.Offer.ID))
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
	if target, err = s.handoffTargetLocked(client, target); err != nil {
		return err
	}
	if strings.EqualFold(notify, client.client.Nick) {
		notify = ""
	}
	h.Updated = s.now().UTC()
	m := s.newMessage(client.client.Nick, target, body, &h.Offer)
	m.Kind, m.AccountID, m.For = r.Action, client.accountID, notify
	if err := s.saveHandoffLocked(h); err != nil {
		return err
	}
	s.postHandoffLocked(client, m, h)
	return nil
}

// postHandoffLocked posts a step's message and answers with the state the
// step produced: a later step may land while this one waits for its sync.
func (s *Server) postHandoffLocked(client *session, m Message, h handoff) {
	mentions := s.recordLocked(&m)
	s.broadcastMessageLocked(m, client.client.Username, mentions)
	s.receiptLocked(client, m, !protocol.IsChannel(m.Target) && s.liveNickLocked(m.Target) == nil)
	s.chatEntryLocked(client, handoffEntry(h))
}

func describeHandoff(h handoff) string {
	switch h.State {
	case handoffAccepted:
		return "already accepted by " + h.Holder
	case handoffDone:
		return "already done by " + h.Holder
	case handoffOpen:
		return "still open"
	}
	return "already " + h.State
}

// listHandoffsLocked answers one handoff by id, every retained handoff in a
// room, or, by default, the open and accepted handoffs that concern client:
// for it, held by it, offered by it, or open to anyone in a room it is in.
// A one-shot agent joins no room, so the rooms it checked, waited or posted
// in within the activity window count as its rooms.
func (s *Server) listHandoffsLocked(client *session, r protocol.ChatRequest) error {
	if r.Target != "" && !validChannel(r.Target) {
		return errors.New("list a room's handoffs by its channel name")
	}
	if r.Limit < 0 || r.Limit > maxHandoffPage {
		return fmt.Errorf("a handoff listing holds at most %d", maxHandoffPage)
	}
	limit := cmp.Or(r.Limit, handoffPage)
	now := s.now()
	_, recent, _ := s.recentLocked(client.client.Nick, now)
	var out []handoff
	for _, h := range sortedHandoffs(s.handoffs) {
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
	for _, h := range out[max(0, len(out)-limit):] {
		s.chatEntryLocked(client, handoffEntry(h))
	}
	return nil
}
