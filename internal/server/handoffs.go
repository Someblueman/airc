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

func (h handoff) id() string           { return h.Offer.ID }
func (h handoff) live() bool           { return h.State == handoffOpen || h.State == handoffAccepted }
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
	case handoffAccepted, handoffDone:
		if !protocol.ValidNick(h.Holder) {
			return errors.New("accepted handoff without a holder")
		}
	default:
		return errors.New("invalid handoff state")
	}
	return nil
}

// handoffState is the open and recent handoffs and their append-only log.
// Every field is read and written with s.mu held; appends and compactions
// run under s.mu (and messageMu, since every step is a post) with env.sync.
type handoffState struct {
	handoffs   map[string]handoff  // by offer ID
	handoffLog *appendLog[handoff] // nil keeps handoffs in memory only
	env        *storeEnv
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
	return s.handoffState.load(path)
}

func (st *handoffState) load(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	loaded, err := readHandoffs(path)
	if err != nil {
		return fmt.Errorf("handoffs %s: %w", path, err)
	}
	if liveRecords(loaded) > maxLiveHandoffs {
		return fmt.Errorf("handoffs %s: more than %d open", path, maxLiveHandoffs)
	}
	st.handoffs = loaded
	st.handoffLog = &appendLog[handoff]{path: path}
	return st.compactHandoffsLocked()
}

// close closes the log; every save completes under s.mu.
func (st *handoffState) close() error {
	if st.handoffLog == nil {
		return nil
	}
	err := st.handoffLog.close()
	st.handoffLog = nil
	return err
}

func readHandoffs(path string) (map[string]handoff, error) {
	return readLedger(path, maxHandoffRecord, validHandoff)
}

// compactHandoffsLocked drops handoffs finished more than a week ago and
// rewrites the log with one line per handoff, then appends to the new file.
func (st *handoffState) compactHandoffsLocked() error {
	return compactLedger(st.handoffLog, st.handoffs, handoffKeep, st.env.now(), st.env.sync)
}

// saveHandoffLocked records h's new state before it takes effect, and
// compacts the log once superseded lines outnumber the handoffs held.
func (st *handoffState) saveHandoffLocked(h handoff) error {
	return recordLedger(st.handoffLog, st.handoffs, h, st.env.sync, st.compactHandoffsLocked, st.env.logger, "handoff_compaction_failed")
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

// handoffRequest offers a handoff (Text to Target, For a nickname when the
// target is a room), steps handoff ID with an optional note, or lists
// handoffs: one by ID, a room's by Target, or up to Limit of the caller's.
type handoffRequest struct {
	Action string
	Target string
	ID     string
	Text   string
	For    string
	Limit  int
}

// decodeHandoffRequest checks the fields of an offer and a listing. An offer
// to a nickname is for that nickname, so For is resolved here.
func decodeHandoffRequest(_ Config, r protocol.ChatRequest) (handoffRequest, error) {
	request := handoffRequest{Action: r.Action, Target: r.Target, ID: r.ID, Text: r.Text, For: r.For, Limit: r.Limit}
	switch r.Action {
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

// chatTargetLocked checks that client may post a handoff or decision step
// (what names which) to target and returns the target as the step's message
// names it.
func (s *Server) chatTargetLocked(client *session, target, what string) (string, error) {
	if !validChannel(target) && !protocol.ValidNick(target) {
		return "", errors.New("a " + what + " needs a room or a nickname")
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
	body, err := s.handoffBody(r.Text, "")
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
	if live := liveRecords(s.handoffs); live >= maxLiveHandoffs {
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

func (s *Server) stepHandoffLocked(client *session, r handoffRequest) error {
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
	if target, err = s.chatTargetLocked(client, target, "handoff"); err != nil {
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
func (s *Server) listHandoffsLocked(client *session, r handoffRequest) error {
	limit := cmp.Or(r.Limit, handoffPage)
	now := s.now()
	_, recent, _ := s.recentLocked(client.client.Nick, now)
	var out []handoff
	for _, h := range sortedRecords(s.handoffs) {
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
