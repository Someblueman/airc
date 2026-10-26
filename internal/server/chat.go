package server

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/protocol"
)

var errChatDenied = errors.New("already reported denial")

func (s *Server) chatEntryLocked(client *session, entry protocol.ChatEntry) {
	data, _ := json.Marshal(entry)
	s.numericLocked(client, "777", nil, string(data))
}

// chatFeatures are the 005 tokens that advertise CHAT and its actions. Every
// one is always advertised, so no action is gated on its token today.
var chatFeatures = []string{"CHAT=1", "CONTEXT=1", "CONTEXT_BYTES=1", "CUSTOM_REACTIONS=1", "HANDOFF=1", "DECISION=1"}

// chatAction is one CHAT action: its handler, whether it never appends to
// history (readOnly, so serializesPosts lets it skip messageMu; a test drives
// each one and checks), and the 005 feature it is advertised under, "" for
// the actions CHAT=1 itself covers.
type chatAction struct {
	name     string
	handler  func(s *Server, client *session, r protocol.ChatRequest) error
	readOnly bool
	requires string
}

// chatActionTable is every CHAT action; chatLocked dispatches from it and
// chatReadOnlyActions is derived from it, so nothing is kept in step by hand.
var chatActionTable = []chatAction{
	{name: "context", handler: typed(decodeContextRequest, (*Server).contextLocked), readOnly: true, requires: "CONTEXT"},
	{name: "pins", handler: typed(decodePinRequest, (*Server).pinsLocked), readOnly: true},
	{name: "pin", handler: typed(decodePinRequest, (*Server).pinsLocked)},
	{name: "unpin", handler: typed(decodePinRequest, (*Server).pinsLocked)},
	{name: "prepare", handler: (*Server).signalLocked, readOnly: true},
	{name: "cancel", handler: (*Server).signalLocked, readOnly: true},
	{name: "waiting", handler: (*Server).signalLocked, readOnly: true},
	{name: "typing", handler: (*Server).signalLocked, readOnly: true},
	{name: "thinking", handler: (*Server).signalLocked, readOnly: true},
	{name: "room", handler: typed(decodeRoomRequest, (*Server).roomLocked)},
	{name: "action", handler: typed(decodePostRequest, (*Server).chatPostLocked)},
	{name: "correct", handler: typed(decodePostRequest, (*Server).chatPostLocked)},
	{name: "retract", handler: typed(decodePostRequest, (*Server).chatPostLocked)},
	{name: "poll", handler: typed(decodePollRequest, (*Server).pollLocked)},
	{name: "vote", handler: typed(decodePollRequest, (*Server).pollLocked)},
	{name: "results", handler: typed(decodePollRequest, (*Server).pollLocked), readOnly: true},
	{name: "close-poll", handler: typed(decodePollRequest, (*Server).pollLocked)},
	{name: "handoff", handler: (*Server).offerHandoffLocked, requires: "HANDOFF"},
	{name: "handoff-accept", handler: typed(decodeHandoffRequest, (*Server).stepHandoffLocked), requires: "HANDOFF"},
	{name: "handoff-blocked", handler: typed(decodeHandoffRequest, (*Server).stepHandoffLocked), requires: "HANDOFF"},
	{name: "handoff-decline", handler: typed(decodeHandoffRequest, (*Server).stepHandoffLocked), requires: "HANDOFF"},
	{name: "handoff-done", handler: typed(decodeHandoffRequest, (*Server).stepHandoffLocked), requires: "HANDOFF"},
	{name: "handoff-withdraw", handler: typed(decodeHandoffRequest, (*Server).stepHandoffLocked), requires: "HANDOFF"},
	{name: "handoffs", handler: typed(decodeHandoffRequest, (*Server).listHandoffsLocked), readOnly: true, requires: "HANDOFF"},
	{name: "decision", handler: (*Server).offerDecisionLocked, requires: "DECISION"},
	{name: "decision-answer", handler: typed(decodeDecisionRequest, (*Server).stepDecisionLocked), requires: "DECISION"},
	{name: "decision-withdraw", handler: typed(decodeDecisionRequest, (*Server).stepDecisionLocked), requires: "DECISION"},
	{name: "decisions", handler: typed(decodeDecisionRequest, (*Server).listDecisionsLocked), readOnly: true, requires: "DECISION"},
}

// chatActions is chatActionTable by name. chatReadOnlyActions are the names
// of its read-only entries, which serializesPosts reads: an action missing
// from it is serialized, which is safe.
var (
	chatActions         = map[string]chatAction{}
	chatReadOnlyActions = map[string]bool{}
)

func init() {
	for _, action := range chatActionTable {
		chatActions[action.name] = action
		if action.readOnly {
			chatReadOnlyActions[action.name] = true
		}
	}
}

func (s *Server) chatLocked(client *session, command protocol.Command) {
	var r protocol.ChatRequest
	if len(command.Trailing) > 7000 || !utf8.ValidString(command.Trailing) || decodeStrict(strings.NewReader(command.Trailing), &r) != nil {
		s.numericLocked(client, "461", nil, "CHAT needs one bounded JSON request")
		return
	}
	// Checked on the union, before the body and the action: it is answered
	// without the rejection prefix and ahead of an unknown action.
	if r.MaxBytes != 0 && r.Action != "context" {
		s.numericLocked(client, "461", nil, "max_bytes is only supported for context")
		return
	}
	if body, exists := command.Tags[protocol.BodyTag]; exists {
		var decodeErr error
		r.Text, decodeErr = protocol.DecodeBody(body)
		if decodeErr != nil {
			s.numericLocked(client, "417", nil, "Invalid chat body")
			return
		}
	}
	var err error
	if action, known := chatActions[r.Action]; known {
		err = action.handler(s, client, r)
	} else {
		err = errors.New("unknown CHAT action")
	}
	if err != nil {
		if !errors.Is(err, errChatDenied) {
			s.numericLocked(client, "461", nil, "Chat request rejected: "+err.Error())
		}
		return
	}
	s.numericLocked(client, "778", nil, "End of chat response")
}

// typed adapts a handler that takes one action group's view of the request
// to the table's signature: decode checks the union's fields as the group
// needs them, with the server's limits, and the handler runs on the result.
// Only checks that come before any state-dependent check or reply in the
// handler belong in a decode, so a request with several faults is answered
// as before.
func typed[T any](decode func(cfg Config, r protocol.ChatRequest) (T, error), handle func(s *Server, client *session, request T) error) func(s *Server, client *session, r protocol.ChatRequest) error {
	return func(s *Server, client *session, r protocol.ChatRequest) error {
		request, err := decode(s.cfg, r)
		if err != nil {
			return err
		}
		return handle(s, client, request)
	}
}

// roomRequest reads or sets a room's slow mode and retention: -1 leaves a
// setting as it is.
type roomRequest struct {
	Target  string
	Seconds int64
	Limit   int
}

func decodeRoomRequest(cfg Config, r protocol.ChatRequest) (roomRequest, error) {
	if !validChannel(r.Target) || r.Seconds < -1 || r.Seconds > 3600 || r.Limit < -1 || r.Limit > cfg.HistoryLimit {
		return roomRequest{}, errors.New("room requires a channel; slow mode 0-3600s; retention 0 through global limit; -1 reads")
	}
	return roomRequest{Target: r.Target, Seconds: r.Seconds, Limit: r.Limit}, nil
}

func (s *Server) roomLocked(client *session, r roomRequest) error {
	if r.Seconds >= 0 || r.Limit >= 0 {
		if !client.admin && !s.channelOperator(client, r.Target) {
			s.numericLocked(client, "481", nil, "Room configuration requires admin or channel operator authentication")
			return errChatDenied
		}
		next := s.copyChat()
		if _, exists := next.Rooms[r.Target]; !exists && len(next.Rooms) >= 128 {
			return errors.New("room settings limit reached")
		}
		room := next.Rooms[r.Target]
		if r.Seconds >= 0 {
			room.SlowSeconds = r.Seconds
		}
		if r.Limit >= 0 {
			room.HistoryLimit = r.Limit
		}
		next.Rooms[r.Target] = room
		if err := s.saveChatLocked(next); err != nil {
			return err
		}
		s.history.trimQuotas()
	}
	room := s.chat.Rooms[r.Target]
	s.chatEntryLocked(client, protocol.ChatEntry{Action: "room", Target: r.Target, SlowSeconds: room.SlowSeconds, HistoryLimit: room.HistoryLimit})
	return nil
}
