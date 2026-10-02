package server

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/protocol"
)

func chatJSON(entry protocol.ChatEntry) string { data, _ := json.Marshal(entry); return string(data) }

func ownsMessage(client *session, m Message) bool {
	if client.admin {
		return true
	}
	if m.AccountID != "" {
		return client.accountID == m.AccountID
	}
	return client.accountID == "" && strings.EqualFold(client.client.Nick, m.From)
}

func (s *Server) annotated(m Message) Message {
	if stored, found := s.history.message(m.ID); found {
		return stored
	}
	return m
}

func (s *Server) chatPostLocked(client *session, r protocol.ChatRequest) error {
	var parent *Message
	if r.Action != "action" {
		m, found := s.history.message(r.ID)
		if !found {
			for _, pins := range s.chat.Pins {
				for _, pin := range pins {
					if pin.ID == r.ID {
						m, found = pin, true
					}
				}
			}
		}
		if !found {
			return errors.New("original message is neither retained nor pinned")
		}
		if !ownsMessage(client, m) {
			return errors.New("only the author or an admin may correct/retract this message")
		}
		r.Target, parent = m.Target, &m
	}
	if !validChannel(r.Target) && !validNick(r.Target) {
		return errors.New("action requires a room or nickname")
	}
	if !s.postAllowedLocked(client, r.Target) {
		return errChatDenied
	}
	body := protocol.NormalizeNewlines(r.Text)
	if body == "" && r.Action == "retract" {
		body = "Retracted"
	}
	if strings.TrimSpace(body) == "" || len(body) > s.cfg.MaxMessageSize || !utf8.ValidString(body) || strings.ContainsRune(body, 0) {
		return errors.New("message must be nonempty valid UTF-8 within the message limit")
	}
	if !client.ephemeral && isChannelName(r.Target) {
		if _, joined := client.channels[r.Target]; !joined {
			return errors.New("join the room before posting")
		}
	}
	if !isChannelName(r.Target) {
		if recipient := s.liveNickLocked(r.Target); recipient != nil && !recipient.observer {
			r.Target = recipient.client.Nick
		} else if s.cfg.HistoryLimit == 0 {
			return errors.New("offline direct messages require history")
		}
	}
	if !s.slowAllowedLocked(client, r.Target) {
		return errChatDenied
	}
	m := s.newMessage(client.client.Nick, r.Target, body, parent)
	m.Kind, m.AccountID = r.Action, client.accountID
	if parent != nil {
		m.Supersedes = parent.ID
	}
	if parent != nil {
		next := s.copyChat()
		for channel, pins := range next.Pins {
			updated := append([]Message{}, pins...)
			for i, p := range updated {
				if p.ID == parent.ID {
					updated[i].SupersededBy, updated[i].Retracted = m.ID, r.Action == "retract"
				}
			}
			next.Pins[channel] = updated
		}
		if err := s.saveChatLocked(next); err != nil {
			return err
		}
	}
	mentions := s.recordLocked(m)
	s.broadcastMessageLocked(m, client.client.Username, mentions)
	s.receiptLocked(client, m, !isChannelName(m.Target) && s.liveNickLocked(m.Target) == nil)
	meta := messageMetadata(m)
	s.chatEntryLocked(client, protocol.ChatEntry{Action: r.Action, Target: m.Target, ID: m.ID, Message: &meta})
	return nil
}
