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

	"github.com/Someblueman/airc/internal/protocol"
)

// A decision is a question put to one named decider, usually a human, with
// optional numbered options. It is answered once, by its decider or an
// admin, or withdrawn by its author. Every step is an ordinary message
// replying to the offer; the state lives in an append-only log of its own,
// as handoffs do, so a decision stays open however long history keeps it.

const (
	decisionOpen      = "open"
	decisionAnswered  = "answered"
	decisionWithdrawn = "withdrawn"
)

// maxLiveDecisions bounds the open decisions kept in memory; answered and
// withdrawn ones are listed for a week. A listing answers the newest
// decisions it selects, 200 unless it asks for up to 1000.
const (
	maxLiveDecisions   = 10000
	decisionKeep       = 7 * 24 * time.Hour
	maxDecisionRecord  = 64 << 10
	decisionPage       = 200
	maxDecisionPage    = 1000
	maxDecisionOptions = 8
	maxDecisionOption  = 80
)

type decision struct {
	Offer   Message   `json:"offer"`
	State   string    `json:"state"`
	Answer  *Message  `json:"answer,omitempty"`
	Updated time.Time `json:"updated"`
}

func (d decision) id() string           { return d.Offer.ID }
func (d decision) live() bool           { return d.State == decisionOpen }
func (d decision) offer() Message       { return d.Offer }
func (d decision) updatedAt() time.Time { return d.Updated }

func validDecision(d decision) error {
	m := d.Offer
	if !protocol.ValidMessageID(m.ID) || m.Kind != "decision" || !protocol.ValidNick(m.From) || !validChannel(m.Target) && !protocol.ValidNick(m.Target) || !protocol.ValidNick(m.For) || d.Updated.IsZero() {
		return errors.New("invalid saved decision")
	}
	if _, err := decisionOptions(m.Choices); err != nil {
		return err
	}
	switch d.State {
	case decisionOpen, decisionWithdrawn:
		if d.Answer != nil {
			return errors.New(d.State + " decision with an answer")
		}
	case decisionAnswered:
		a := d.Answer
		if a == nil || !protocol.ValidMessageID(a.ID) || a.ReplyTo != m.ID || !protocol.ValidNick(a.From) || a.Choice < 0 || a.Choice > len(m.Choices) {
			return errors.New("answered decision without a valid answer")
		}
	default:
		return errors.New("invalid decision state")
	}
	return nil
}

// decisionOptions checks a decision's options: none, or two to eight
// distinct short lines, trimmed.
func decisionOptions(options []string) ([]string, error) {
	switch {
	case len(options) == 0:
		return nil, nil
	case len(options) == 1:
		return nil, errors.New("a decision with options needs at least two")
	case len(options) > maxDecisionOptions:
		return nil, fmt.Errorf("a decision has at most %d options", maxDecisionOptions)
	}
	out := make([]string, len(options))
	for i, option := range options {
		option = strings.TrimSpace(option)
		if option == "" || !protocol.BriefText(option, maxDecisionOption) || slices.Contains(out[:i], option) {
			return nil, fmt.Errorf("options must be distinct nonempty text up to %d bytes", maxDecisionOption)
		}
		out[i] = option
	}
	return out, nil
}

// decisionState is the open and recent decisions and their append-only
// log. Every field is read and written with s.mu held; appends and
// compactions run under s.mu (and messageMu, since every step is a post)
// with env.sync.
type decisionState struct {
	decisions   map[string]decision  // by offer ID
	decisionLog *appendLog[decision] // nil keeps decisions in memory only
	env         *storeEnv
}

// RestoreDecisions loads the decision log at path, keeping every open
// decision and finished ones for a week, compacts it and appends to it from
// then on. A torn last line, from a crash mid-write, is dropped.
func (s *Server) RestoreDecisions(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil || s.decisionLog != nil {
		return errors.New("configure decisions once before Serve")
	}
	return s.decisionState.load(path)
}

func (st *decisionState) load(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	loaded, err := readDecisions(path)
	if err != nil {
		return fmt.Errorf("decisions %s: %w", path, err)
	}
	if liveRecords(loaded) > maxLiveDecisions {
		return fmt.Errorf("decisions %s: more than %d open", path, maxLiveDecisions)
	}
	st.decisions = loaded
	st.decisionLog = &appendLog[decision]{path: path}
	return st.compactDecisionsLocked()
}

// close closes the log; every save completes under s.mu.
func (st *decisionState) close() error {
	if st.decisionLog == nil {
		return nil
	}
	err := st.decisionLog.close()
	st.decisionLog = nil
	return err
}

func readDecisions(path string) (map[string]decision, error) {
	return readLedger(path, maxDecisionRecord, validDecision)
}

// compactDecisionsLocked drops decisions finished more than a week ago and
// rewrites the log with one line per decision, then appends to the new file.
func (st *decisionState) compactDecisionsLocked() error {
	return compactLedger(st.decisionLog, st.decisions, decisionKeep, st.env.now(), st.env.sync)
}

// saveDecisionLocked records d's new state before it takes effect, and
// compacts the log once superseded lines outnumber the decisions held.
func (st *decisionState) saveDecisionLocked(d decision) error {
	return recordLedger(st.decisionLog, st.decisions, d, st.env.sync, st.compactDecisionsLocked, st.env.logger, "decision_compaction_failed")
}

// decisionEntryLocked is d as a CHAT entry: the offer, the answer once there
// is one, and the asker's directory card, whose kind tells a decider whether
// a human is waiting without a lookup of its own.
func (s *Server) decisionEntryLocked(d decision) protocol.ChatEntry {
	meta := messageMetadata(d.Offer)
	card := s.directoryCard(d.Offer.From, s.now().UTC())
	entry := protocol.ChatEntry{Action: "decision", Target: d.Offer.Target, ID: d.Offer.ID, From: d.Offer.From, AccountID: d.Offer.AccountID, State: d.State, Message: &meta, Profile: &card}
	if d.Answer != nil {
		answer := messageMetadata(*d.Answer)
		entry.Answer = &answer
	}
	return entry
}

// decisionRequest asks For to decide (Text, with Options to choose from, in
// Target), answers decision ID with Choice and a note, withdraws it, or
// lists decisions: one by ID, a room's by Target, or up to Limit of the
// caller's.
type decisionRequest struct {
	Action  string
	Target  string
	ID      string
	Text    string
	For     string
	Options []string
	Choice  int
	Limit   int
}

// decodeDecisionRequest checks the fields of an offer and a listing. An offer
// to a nickname is for that nickname, so For is resolved here.
func decodeDecisionRequest(_ Config, r protocol.ChatRequest) (decisionRequest, error) {
	request := decisionRequest{Action: r.Action, Target: r.Target, ID: r.ID, Text: r.Text, For: r.For, Options: r.Options, Choice: r.Choice, Limit: r.Limit}
	switch r.Action {
	case "decisions":
		if r.Target != "" && !validChannel(r.Target) {
			return decisionRequest{}, errors.New("list a room's decisions by its channel name")
		}
		if r.Limit < 0 || r.Limit > maxDecisionPage {
			return decisionRequest{}, fmt.Errorf("a decision listing holds at most %d", maxDecisionPage)
		}
	case "decision":
		if !protocol.IsChannel(r.Target) {
			if r.For != "" && !strings.EqualFold(r.For, r.Target) {
				return decisionRequest{}, errors.New("a direct decision is for the nickname it is sent to")
			}
			request.For = r.Target
		}
		if request.For == "" {
			return decisionRequest{}, errors.New("a decision names who decides")
		}
		if !protocol.ValidNick(request.For) {
			return decisionRequest{}, errors.New("a decision is for a valid nickname")
		}
	}
	return request, nil
}

// offerDecisionLocked is the table's handler for an offer; the decision
// tests drive it with the wire request.
func (s *Server) offerDecisionLocked(client *session, r protocol.ChatRequest) error {
	request, err := decodeDecisionRequest(s.cfg, r)
	if err != nil {
		return err
	}
	return s.openDecisionLocked(client, request)
}

func (s *Server) openDecisionLocked(client *session, r decisionRequest) error {
	decider := r.For
	if strings.EqualFold(decider, client.client.Nick) {
		return errors.New("ask someone else to decide")
	}
	options, err := decisionOptions(r.Options)
	if err != nil {
		return err
	}
	body, err := s.handoffBody(r.Text, "")
	if err != nil {
		return err
	}
	target, err := s.chatTargetLocked(client, r.Target, "decision")
	if err != nil {
		return err
	}
	if !protocol.IsChannel(target) {
		decider = target
	}
	if !s.slowAllowedLocked(client, target) {
		return errChatDenied
	}
	if live := liveRecords(s.decisions); live >= maxLiveDecisions {
		return fmt.Errorf("%d decisions are open; answer or withdraw some first", live)
	}
	m := s.postMessage(client, target, body, nil)
	m.Kind, m.AccountID, m.For, m.Choices = "decision", client.accountID, decider, options
	d := decision{Offer: m, State: decisionOpen, Updated: m.Timestamp}
	if err := s.saveDecisionLocked(d); err != nil {
		return err
	}
	s.postDecisionLocked(client, m, d)
	return nil
}

func (s *Server) stepDecisionLocked(client *session, r decisionRequest) error {
	d, found := s.decisions[r.ID]
	if !found {
		return errors.New("decision is not retained")
	}
	author := authored(client, d.Offer)
	// A step repeated after its answer was lost answers again, unposted.
	if r.Action == "decision-answer" && d.Answer != nil && authored(client, *d.Answer) ||
		r.Action == "decision-withdraw" && d.State == decisionWithdrawn && author {
		s.chatEntryLocked(client, s.decisionEntryLocked(d))
		return nil
	}
	if d.State != decisionOpen {
		return fmt.Errorf("decision is %s", describeDecision(d))
	}
	notify := d.Offer.From // who the step is for
	note := strings.TrimSpace(protocol.NormalizeNewlines(r.Text))
	var body string
	switch r.Action {
	case "decision-answer":
		if !client.admin && !s.isNickLocked(client, d.Offer.For) {
			return errors.New("this decision is for " + d.Offer.For)
		}
		option := ""
		switch {
		case len(d.Offer.Choices) == 0 && r.Choice != 0:
			return errors.New("this decision has no options; answer with a note")
		case r.Choice < 0 || r.Choice > len(d.Offer.Choices):
			return fmt.Errorf("choose an option from 1 to %d", len(d.Offer.Choices))
		case r.Choice == 0 && note == "" && len(d.Offer.Choices) > 0:
			return errors.New("answer with an option number, a note, or both")
		case r.Choice == 0 && note == "":
			return errors.New("answer with a note")
		case r.Choice > 0:
			option = d.Offer.Choices[r.Choice-1]
		}
		body = protocol.DecisionStepText(r.Action, d.Offer.ID, r.Choice, option, note)
		d.State = decisionAnswered
	case "decision-withdraw":
		if !ownsMessage(client, d.Offer) {
			return errors.New("only the author or an admin may withdraw this decision")
		}
		notify = d.Offer.For
		body = protocol.DecisionStepText(r.Action, d.Offer.ID, 0, "", note)
		d.State = decisionWithdrawn
	default:
		return errors.New("unknown CHAT action")
	}
	body, err := s.handoffBody(body, "")
	if err != nil {
		return err
	}
	target := d.Offer.Target
	if !protocol.IsChannel(target) {
		// A direct decision's steps go to the other side.
		target = d.Offer.From
		if strings.EqualFold(client.client.Nick, d.Offer.From) {
			target = d.Offer.For
		}
	}
	if target, err = s.chatTargetLocked(client, target, "decision"); err != nil {
		return err
	}
	if strings.EqualFold(notify, client.client.Nick) {
		notify = ""
	}
	d.Updated = s.now().UTC()
	m := s.postMessage(client, target, body, &d.Offer)
	m.Kind, m.AccountID, m.For = r.Action, client.accountID, notify
	if r.Action == "decision-answer" {
		m.Choice = r.Choice
		answer := m
		d.Answer = &answer
	}
	if err := s.saveDecisionLocked(d); err != nil {
		return err
	}
	s.postDecisionLocked(client, m, d)
	return nil
}

// postDecisionLocked posts a step's message and answers with the state the
// step produced: a later step may land while this one waits for its sync.
func (s *Server) postDecisionLocked(client *session, m Message, d decision) {
	mentions := s.recordLocked(&m)
	s.broadcastMessageLocked(m, client.client.Username, mentions)
	s.receiptLocked(client, m, !protocol.IsChannel(m.Target) && s.liveNickLocked(m.Target) == nil)
	s.chatEntryLocked(client, s.decisionEntryLocked(d))
}

func describeDecision(d decision) string {
	switch {
	case d.State == decisionAnswered && d.Answer != nil:
		return "already answered by " + d.Answer.From
	case d.State == decisionOpen:
		return "still open"
	}
	return "already " + d.State
}

// listDecisionsLocked answers one decision by id, every retained decision
// in a room, or, by default, the open decisions that concern client: for it
// to decide, asked by it, or asked in a room it is in. A one-shot agent
// joins no room, so the rooms it checked, waited or posted in within the
// activity window count as its rooms.
func (s *Server) listDecisionsLocked(client *session, r decisionRequest) error {
	limit := cmp.Or(r.Limit, decisionPage)
	now := s.now()
	_, recent, _ := s.recentLocked(client.client.Nick, now)
	var out []decision
	for _, d := range sortedRecords(s.decisions) {
		if !d.live() && now.Sub(d.Updated) > decisionKeep {
			continue
		}
		switch {
		case r.ID != "":
			if d.Offer.ID != r.ID {
				continue
			}
		case r.Target != "":
			if d.Offer.Target != r.Target {
				continue
			}
		default:
			if !d.live() {
				continue
			}
			_, joined := client.channels[d.Offer.Target]
			joined = joined || slices.Contains(recent, d.Offer.Target)
			if !s.isNickLocked(client, d.Offer.For) && !authored(client, d.Offer) && !joined {
				continue
			}
		}
		out = append(out, d)
	}
	if r.ID != "" && len(out) == 0 {
		return errors.New("decision is not retained")
	}
	for _, d := range out[max(0, len(out)-limit):] {
		s.chatEntryLocked(client, s.decisionEntryLocked(d))
	}
	return nil
}
