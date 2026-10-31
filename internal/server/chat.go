package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/protocol"
)

var errChatDenied = errors.New("already reported denial")

func (s *Server) chatEntryLocked(client *session, entry protocol.ChatEntry) {
	data, _ := json.Marshal(entry)
	s.numericLocked(client, "777", nil, string(data))
}

// chatAction is one CHAT action: its handler, whether it never appends to
// history (readOnly, so serializesPosts lets it skip messageMu; a test drives
// each one and checks), and what kind of ID its id field names, so
// chatLocked resolves an abbreviated one before the handler reads it (0 for
// an action without an ID). Every action comes with the AIRC level in 005,
// so none is gated on a feature of its own.
type chatAction struct {
	name     string
	handler  func(s *Server, client *session, r protocol.ChatRequest) error
	readOnly bool
	ids      idStore
}

// chatActionTable is every CHAT action; chatLocked dispatches from it and
// chatReadOnlyActions is derived from it, so nothing is kept in step by hand.
var chatActionTable = []chatAction{
	{name: "context", handler: typed(decodeContextRequest, (*Server).contextLocked), readOnly: true, ids: messageIDStore},
	{name: "pins", handler: typed(decodePinRequest, (*Server).pinsLocked), readOnly: true},
	{name: "pin", handler: typed(decodePinRequest, (*Server).pinsLocked), ids: messageIDStore},
	{name: "unpin", handler: typed(decodePinRequest, (*Server).pinsLocked), ids: messageIDStore},
	{name: "prepare", handler: (*Server).signalLocked, readOnly: true, ids: messageIDStore},
	{name: "cancel", handler: (*Server).signalLocked, readOnly: true, ids: messageIDStore},
	{name: "waiting", handler: (*Server).signalLocked, readOnly: true, ids: messageIDStore},
	{name: "typing", handler: (*Server).signalLocked, readOnly: true},
	{name: "thinking", handler: (*Server).signalLocked, readOnly: true},
	{name: "room", handler: typed(decodeRoomRequest, (*Server).roomLocked)},
	{name: "action", handler: typed(decodePostRequest, (*Server).chatPostLocked)},
	{name: "correct", handler: typed(decodePostRequest, (*Server).chatPostLocked), ids: messageIDStore},
	{name: "retract", handler: typed(decodePostRequest, (*Server).chatPostLocked), ids: messageIDStore},
	{name: "status", handler: typed(decodeStatusRequest, (*Server).postStatusLocked)},
	{name: "statuses", handler: typed(decodeStatusRequest, (*Server).listStatusesLocked), readOnly: true},
	{name: "resolve", handler: typed(decodeResolveRequest, (*Server).resolveLocked), ids: messageIDStore},
	{name: "poll", handler: typed(decodePollRequest, (*Server).pollLocked)},
	{name: "vote", handler: typed(decodePollRequest, (*Server).pollLocked), ids: messageIDStore},
	{name: "results", handler: typed(decodePollRequest, (*Server).pollLocked), readOnly: true, ids: messageIDStore},
	{name: "close-poll", handler: typed(decodePollRequest, (*Server).pollLocked), ids: messageIDStore},
	{name: "handoff", handler: (*Server).offerHandoffLocked},
	{name: "handoff-accept", handler: typed(decodeHandoffRequest, (*Server).stepHandoffLocked), ids: messageIDStore},
	{name: "handoff-blocked", handler: typed(decodeHandoffRequest, (*Server).stepHandoffLocked), ids: messageIDStore},
	{name: "handoff-decline", handler: typed(decodeHandoffRequest, (*Server).stepHandoffLocked), ids: messageIDStore},
	{name: "handoff-done", handler: typed(decodeHandoffRequest, (*Server).stepHandoffLocked), ids: messageIDStore},
	{name: "handoff-withdraw", handler: typed(decodeHandoffRequest, (*Server).stepHandoffLocked), ids: messageIDStore},
	{name: "handoffs", handler: typed(decodeHandoffRequest, (*Server).listHandoffsLocked), readOnly: true, ids: messageIDStore},
	{name: "decision", handler: (*Server).offerDecisionLocked},
	{name: "decision-answer", handler: typed(decodeDecisionRequest, (*Server).stepDecisionLocked), ids: messageIDStore},
	{name: "decision-withdraw", handler: typed(decodeDecisionRequest, (*Server).stepDecisionLocked), ids: messageIDStore},
	{name: "decisions", handler: typed(decodeDecisionRequest, (*Server).listDecisionsLocked), readOnly: true, ids: messageIDStore},
	// Pastes never append to history: an upload writes the paste store, and
	// a message sharing a paste is an ordinary PRIVMSG.
	{name: "paste-begin", handler: typed(decodePasteRequest, (*Server).uploadPasteLocked), readOnly: true},
	{name: "paste-chunk", handler: typed(decodePasteRequest, (*Server).uploadPasteLocked), readOnly: true},
	{name: "paste-end", handler: typed(decodePasteRequest, (*Server).uploadPasteLocked), readOnly: true},
	{name: "paste-get", handler: typed(decodePasteRequest, (*Server).getPasteLocked), readOnly: true, ids: pasteIDStore},
	{name: "pastes", handler: typed(decodePasteRequest, (*Server).listPastesLocked), readOnly: true, ids: pasteIDStore},
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
		if action.ids != 0 && r.ID != "" {
			// An abbreviated id names the retained ID it is a prefix of;
			// one nothing matches goes to the handler as given, whose
			// lookup answers that it is not retained.
			if r.ID, err = s.resolveIDLocked(r.ID, action.ids); err != nil {
				s.numericLocked(client, "461", nil, err.Error())
				return
			}
		}
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

// roomRequest reads or sets a room's slow mode, retention and charter: -1
// leaves a setting as it is, and a nil Charter leaves the charter.
type roomRequest struct {
	Target  string
	Seconds int64
	Limit   int
	Charter *string
}

func decodeRoomRequest(cfg Config, r protocol.ChatRequest) (roomRequest, error) {
	if !validChannel(r.Target) || r.Seconds < -1 || r.Seconds > 3600 || r.Limit < -1 || r.Limit > cfg.HistoryLimit {
		return roomRequest{}, errors.New("room requires a channel; slow mode 0-3600s; retention 0 through global limit; -1 reads")
	}
	request := roomRequest{Target: r.Target, Seconds: r.Seconds, Limit: r.Limit}
	if r.Charter != nil {
		charter := strings.TrimSpace(protocol.NormalizeNewlines(*r.Charter))
		if !protocol.ValidCharter(charter) {
			return roomRequest{}, fmt.Errorf("a charter is UTF-8 text of at most %d bytes", protocol.MaxCharterBytes)
		}
		request.Charter = &charter
	}
	return request, nil
}

func (s *Server) roomLocked(client *session, r roomRequest) error {
	changing := r.Seconds >= 0 || r.Limit >= 0 || r.Charter != nil
	if changing && !client.admin && !s.channelOperator(client, r.Target) {
		if r.Charter != nil {
			s.numericLocked(client, "481", nil, "only a room operator or an admin may set the charter")
		} else {
			s.numericLocked(client, "481", nil, "Room configuration requires admin or channel operator authentication")
		}
		return errChatDenied
	}
	if changing {
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
		charterChanged := r.Charter != nil && *r.Charter != room.Charter
		if r.Charter != nil {
			room.Charter = *r.Charter
		}
		if room == (roomSettings{}) {
			delete(next.Rooms, r.Target)
		} else {
			next.Rooms[r.Target] = room
		}
		if err := s.saveChatLocked(next); err != nil {
			return err
		}
		s.history.trimQuotas()
		if charterChanged {
			// A live signal, so an open UI can say the rules changed; a
			// one-shot check compares the charter itself.
			s.broadcastSignalLocked(client, protocol.ChatEntry{Action: "charter", Target: r.Target, From: client.client.Nick, ExpiresAt: s.now().UTC().Add(time.Minute)}, Message{})
		}
		s.logger.Info("room_configured", "nick", client.client.Nick, "channel", r.Target, "charter_changed", charterChanged)
	}
	room := s.chat.Rooms[r.Target]
	s.chatEntryLocked(client, protocol.ChatEntry{Action: "room", Target: r.Target, SlowSeconds: room.SlowSeconds, HistoryLimit: room.HistoryLimit, Charter: room.Charter})
	return nil
}
