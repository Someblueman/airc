package server

import (
	"errors"
	"maps"
	"strings"

	"github.com/Someblueman/airc/internal/protocol"
)

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
	retained := false
	h.forward(h.selectTarget(target), 0, func(uint64, *entry) bool {
		retained = true
		return false
	})
	if retained {
		return target, nil
	}
	return "", errors.New("message or conversation is no longer retained")
}

func (s *Server) replyLocked(client *session, command protocol.Command) {
	id, _ := command.Param(0)
	id, err := s.resolveIDLocked(id, messageIDStore)
	if err != nil {
		s.numericLocked(client, "461", nil, err.Error())
		return
	}
	parent, found := s.history.message(id)
	if !protocol.ValidMessageID(id) || !found {
		s.numericLocked(client, "430", []string{id}, "reply parent is not retained; read the conversation and reply to a retained message")
		return
	}
	target := parent.Target
	if !protocol.IsChannel(target) {
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
		var repeated *Message
		s.history.backward(selection{lists: s.history.lists("child:" + id)}, func(_ uint64, e *entry) bool {
			if e.Reaction == command.Trailing && strings.EqualFold(e.From, client.client.Nick) && e.AccountID == client.accountID {
				repeated = &e.Message
			}
			return repeated == nil
		})
		if repeated != nil {
			s.receiptLocked(client, *repeated, s.queuedLocked(*repeated))
			return
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
		maps.Copy(readers, group)
	}
	if protocol.IsChannel(message.Target) {
		add(s.channels[message.Target])
		add(s.watchers[message.Target])
		for _, nick := range mentions {
			if !strings.EqualFold(nick, message.From) {
				add(s.watchers["@"+nick])
			}
		}
	} else {
		if recipient := s.liveNickLocked(message.Target); recipient != nil && !recipient.observer {
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
		if protocol.IsChannel(message.Target) {
			if _, banned := s.restrictionLocked("ban", reader.client.Nick, message.Target); banned {
				continue
			}
		}
		reader.enqueue(line)
	}
	s.noteInboxLocked(message, mentions)
}
