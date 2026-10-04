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

func (s *Server) chatLocked(client *session, command protocol.Command) {
	var r protocol.ChatRequest
	if len(command.Trailing) > 7000 || !utf8.ValidString(command.Trailing) || decodeStrict(strings.NewReader(command.Trailing), &r) != nil {
		s.numericLocked(client, "461", nil, "CHAT needs one bounded JSON request")
		return
	}
	if r.MaxBytes != 0 && r.Action != "context" {
		s.numericLocked(client, "461", nil, "max_bytes is only supported for context")
		return
	}
	var err error
	if body, exists := command.Tags[protocol.BodyTag]; exists {
		var decodeErr error
		r.Text, decodeErr = protocol.DecodeBody(body)
		if decodeErr != nil {
			s.numericLocked(client, "417", nil, "Invalid chat body")
			return
		}
	}
	switch r.Action {
	case "context":
		err = s.contextLocked(client, r)
	case "pins", "pin", "unpin":
		err = s.pinsLocked(client, r)
	case "prepare", "cancel", "waiting", "typing", "thinking":
		err = s.signalLocked(client, r)
	case "room":
		err = s.roomLocked(client, r)
	case "action", "correct", "retract":
		err = s.chatPostLocked(client, r)
	case "poll", "vote", "results", "close-poll":
		err = s.pollLocked(client, r)
	default:
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

func (s *Server) roomLocked(client *session, r protocol.ChatRequest) error {
	if !validChannel(r.Target) || r.Seconds < -1 || r.Seconds > 3600 || r.Limit < -1 || r.Limit > s.cfg.HistoryLimit {
		return errors.New("room requires a channel; slow mode 0-3600s; retention 0 through global limit; -1 reads")
	}
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
