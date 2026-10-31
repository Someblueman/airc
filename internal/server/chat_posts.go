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

// chatBody is the body of a structured post: text with its line endings
// normalised, or fallback when text is blank, checked as every message is.
func (s *Server) chatBody(text, fallback string) (string, error) {
	body := protocol.NormalizeNewlines(text)
	if strings.TrimSpace(body) == "" {
		body = fallback
	}
	if strings.TrimSpace(body) == "" || len(body) > s.cfg.MaxMessageSize || !utf8.ValidString(body) || strings.ContainsRune(body, 0) {
		return "", errors.New("message must be nonempty valid UTF-8 within the message limit")
	}
	return body, nil
}

// chatTargetLocked checks that client may post a structured message (what
// names which: a handoff or decision step, a status, a resolution) to
// target and returns the target as the message names it.
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

// queuedLocked reports whether m is a direct message kept for a recipient
// that is not connected, for its receipt.
func (s *Server) queuedLocked(m Message) bool {
	return !protocol.IsChannel(m.Target) && s.liveNickLocked(m.Target) == nil
}

// postStructuredLocked records a structured post whose checks passed and
// whose state is saved, delivers it and confirms it to client. The caller
// answers with the CHAT entry the post produced. PRIVMSG has its own path
// (messageLocked): it confirms a room message only to one-shot senders and
// reactions, and shares a paste.
func (s *Server) postStructuredLocked(client *session, m Message) {
	mentions := s.recordLocked(&m)
	s.broadcastMessageLocked(m, client.client.Username, mentions)
	s.receiptLocked(client, m, s.queuedLocked(m))
}

// postRequest posts Text as an action in Target, or as a correction or
// retraction of message ID, whose target it takes. Every check depends on
// the retained message or the poster's standing, so none is made here.
type postRequest struct {
	Action string
	Target string
	ID     string
	Text   string
}

func decodePostRequest(_ Config, r protocol.ChatRequest) (postRequest, error) {
	return postRequest{Action: r.Action, Target: r.Target, ID: r.ID, Text: r.Text}, nil
}

func (s *Server) chatPostLocked(client *session, r postRequest) error {
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
	if !validChannel(r.Target) && !protocol.ValidNick(r.Target) {
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
	if !client.ephemeral && protocol.IsChannel(r.Target) {
		if _, joined := client.channels[r.Target]; !joined {
			return errors.New("join the room before posting")
		}
	}
	if !protocol.IsChannel(r.Target) {
		if recipient := s.liveNickLocked(r.Target); recipient != nil && !recipient.observer {
			r.Target = recipient.client.Nick
		} else if s.cfg.HistoryLimit == 0 {
			return errors.New("offline direct messages require history")
		}
	}
	if !s.slowAllowedLocked(client, r.Target) {
		return errChatDenied
	}
	m := s.postMessage(client, r.Target, body, parent)
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
	s.postStructuredLocked(client, m)
	meta := messageMetadata(m)
	s.chatEntryLocked(client, protocol.ChatEntry{Action: r.Action, Target: m.Target, ID: m.ID, Message: &meta})
	return nil
}
