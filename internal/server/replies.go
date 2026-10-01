package server

import (
	"errors"
	"strings"

	"github.com/Someblueman/airc/internal/protocol"
)

func (h *historyRing) message(id string) (Message, bool) {
	index, ok := h.positions[id]
	if !ok {
		return Message{}, false
	}
	return h.items[index], true
}

// conversation resolves a child ID to its root. Descendants remain readable
// after a root is evicted, without keeping any extra history outside the ring.
func (h *historyRing) conversation(target string) (string, error) {
	id, replies, ok := protocol.ConversationTarget(target)
	if !ok {
		return "", errors.New("conversation requires a valid message ID")
	}
	if message, found := h.message(id); found {
		if !replies && message.ThreadID != "" {
			return "thread:" + message.ThreadID, nil
		}
		return target, nil
	}
	for i := 0; i < h.size; i++ {
		message := h.at(i)
		if !replies && message.ThreadID == id || replies && message.ReplyTo == id && message.Reaction == "" {
			return target, nil
		}
	}
	return "", errors.New("message or conversation is no longer retained")
}

func (s *Server) replyLocked(client *session, command protocol.Command) {
	id, _ := command.Param(0)
	parent, found := s.history.message(id)
	if !protocol.ValidMessageID(id) || !found {
		s.numericLocked(client, "430", []string{id}, "reply parent is not retained; read the conversation and reply to a retained message")
		return
	}
	target := parent.Target
	if !isChannelName(target) {
		switch {
		case strings.EqualFold(client.client.Nick, parent.From):
		case strings.EqualFold(client.client.Nick, parent.Target):
			target = parent.From
		default:
			s.numericLocked(client, "484", nil, "only the participants may reply to a direct message")
			return
		}
	}
	if !s.postAllowedLocked(client, target) {
		return
	}
	if command.Name == "REACT" {
		if !protocol.ValidReaction(command.Trailing) || len(command.Tags) > 0 {
			s.numericLocked(client, "461", nil, "REACT requires one symbol up to 32 bytes without body tags")
			return
		}
		// Repeating a retained reaction from this nickname is idempotent.
		for i := s.history.size - 1; i >= 0; i-- {
			message := s.history.at(i)
			if message.ReplyTo == id && message.Reaction == command.Trailing && strings.EqualFold(message.From, client.client.Nick) && message.AccountID == client.accountID {
				s.receiptLocked(client, message, !isChannelName(message.Target) && s.nicks[nickKey(message.Target)] == nil)
				return
			}
		}
	}
	command.Params[0] = target
	s.messageLocked(client, command, false, &parent)
}

// A message may reach a reader through its room, inbox, thread and parent.
// Collect recipients once so overlapping subscriptions never repeat it.
func (s *Server) broadcastMessageLocked(message Message, username string, mentions []string) {
	readers := make(map[string]*session)
	add := func(group map[string]*session) {
		for id, reader := range group {
			readers[id] = reader
		}
	}
	if isChannelName(message.Target) {
		add(s.channels[message.Target])
		add(s.watchers[message.Target])
		for _, nick := range mentions {
			if !strings.EqualFold(nick, message.From) {
				add(s.watchers["@"+nick])
			}
		}
	} else {
		if recipient := s.nicks[nickKey(message.Target)]; recipient != nil && !recipient.observer {
			readers[recipient.client.ID] = recipient
		}
		add(s.watchers["@"+nickKey(message.Target)])
		add(s.watchers[protocol.AllDirectMessages])
	}
	if message.ReplyTo != "" {
		if message.Reaction == "" {
			add(s.watchers["replies:"+message.ReplyTo])
		}
		add(s.watchers["thread:"+message.ThreadID])
	}
	line := formatMessage(message, username)
	for _, reader := range readers {
		if isChannelName(message.Target) {
			if _, banned := s.restrictionLocked("ban", reader.client.Nick, message.Target); banned {
				continue
			}
		}
		reader.enqueue(line)
	}
}
