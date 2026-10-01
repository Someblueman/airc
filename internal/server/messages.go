package server

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/protocol"
)

func (s *Server) messageLocked(client *session, command protocol.Command, notice bool, parent *Message) {
	targets, ok := command.Param(0)
	if !ok || len(command.Params) == 0 {
		if !notice {
			s.numericLocked(client, "461", []string{"PRIVMSG"}, "Not enough parameters")
		}
		return
	}
	body := command.Trailing
	if body == "" && len(command.Params) > 1 {
		body = strings.Join(command.Params[1:], " ")
	}
	if encoded, tagged := command.Tags[protocol.BodyTag]; tagged {
		// A multi-line body travels whole in the tag; the trailing text is only a preview.
		decoded, err := protocol.DecodeBody(encoded)
		if err != nil {
			if !notice {
				s.numericLocked(client, "417", nil, "Malformed message body")
			}
			return
		}
		body = decoded
	}
	if strings.TrimSpace(body) == "" {
		if !notice {
			s.numericLocked(client, "412", nil, "No text to send")
		}
		return
	}
	if len(body) > s.cfg.MaxMessageSize || !utf8.ValidString(body) {
		if !notice {
			s.numericLocked(client, "417", nil, "Message is too long or is not valid UTF-8")
		}
		return
	}
	targetList := strings.Split(targets, ",")
	if len(targetList) > 16 {
		if !notice {
			s.numericLocked(client, "407", nil, "Too many targets")
		}
		return
	}
	for _, target := range targetList {
		if s.closing.Load() {
			return
		}
		if !s.postAllowedLocked(client, target) {
			continue
		}
		if s.retryLocked(client, command, target, body, parent) {
			continue
		}
		if isChannelName(target) {
			if client.ephemeral {
				// One-shot senders never join, so any well-formed channel is a valid
				// destination even when nobody is connected to it right now.
				if !validChannel(target) {
					if !notice {
						s.numericLocked(client, "403", []string{target}, "No such channel")
					}
					continue
				}
			} else {
				if s.channels[target] == nil {
					if !notice {
						s.numericLocked(client, "403", []string{target}, "No such channel")
					}
					continue
				}
				if _, joined := client.channels[target]; !joined {
					if !notice {
						s.numericLocked(client, "404", []string{target}, "Cannot send to channel")
					}
					continue
				}
			}
			if command.Name != "REACT" && !s.slowAllowedLocked(client, target) {
				continue
			}
			message := s.newMessage(client.client.Nick, target, body, parent)
			message.AccountID, message.RequestID = client.accountID, command.Tags[protocol.RequestIDTag]
			if command.Name == "REACT" {
				message.Reaction = body
			}
			mentions := s.recordLocked(message)
			s.broadcastMessageLocked(message, client.client.Username, mentions)
			if client.ephemeral || command.Name == "REACT" {
				s.receiptLocked(client, message, false)
			}
			s.logger.Info("message_sent", "id", message.ID, "from", message.From, "target", target)
			continue
		}
		recipient := s.nicks[nickKey(target)]
		live := recipient != nil && !recipient.observer
		if !live && (notice || s.cfg.HistoryLimit == 0 || !validNick(target)) {
			// Without history there is nowhere to hold the message for a later read.
			if !notice {
				s.numericLocked(client, "401", []string{target}, "No such nick")
			}
			continue
		}
		to := target
		if live {
			to = recipient.client.Nick
		}
		message := s.newMessage(client.client.Nick, to, body, parent)
		message.AccountID, message.RequestID = client.accountID, command.Tags[protocol.RequestIDTag]
		if command.Name == "REACT" {
			message.Reaction = body
		}
		s.recordLocked(message)
		s.broadcastMessageLocked(message, client.client.Username, nil)
		s.receiptLocked(client, message, !live)
		s.logger.Info("message_sent", "id", message.ID, "from", message.From, "target", to, "queued", !live)
	}
}

// receiptLocked confirms a stored message to its sender. queued means a direct
// message was kept for a recipient that is not currently connected.
func (s *Server) receiptLocked(client *session, message Message, queued bool) {
	params := []string{message.Target}
	if queued {
		params = append(params, "queued")
	}
	s.numericLocked(client, "762", params, encodeMessage(message))
}

func encodeMessage(message Message) string {
	return protocol.EncodeMessageMetadata(messageMetadata(message))
}

func messageMetadata(message Message) protocol.MessageMetadata {
	return protocol.MessageMetadata{ChatMetadata: message.ChatMetadata, ID: message.ID, ReplyTo: message.ReplyTo, ThreadID: message.ThreadID, Reaction: message.Reaction, Seq: message.Seq, From: message.From, Target: message.Target, Message: message.Body, Timestamp: message.Timestamp}
}

func (s *Server) newMessage(from, target, body string, parent *Message) Message {
	s.seq++
	message := Message{ID: newID(), Seq: s.seq, From: from, Target: target, Body: body, Timestamp: time.Now().UTC()}
	if parent != nil {
		message.ReplyTo, message.ThreadID = parent.ID, parent.ThreadID
		if message.ThreadID == "" {
			message.ThreadID = parent.ID
		}
	}
	return message
}

func formatMessage(message Message, username string) string {
	tags := "@msgid=" + message.ID + ";time=" + protocol.EscapeTag(message.Timestamp.Format(time.RFC3339Nano))
	if message.AccountID != "" || message.RequestID != "" || message.Kind != "" {
		tags += ";" + protocol.ChatTag + "=" + protocol.EncodeChat(message.ChatMetadata)
	}
	if message.ReplyTo != "" {
		tags += ";" + protocol.ReplyTag + "=" + message.ReplyTo + ";" + protocol.ThreadTag + "=" + message.ThreadID
	}
	if message.Reaction != "" {
		tags += ";" + protocol.ReactionTag + "=" + protocol.EscapeTag(message.Reaction)
	}
	if strings.Contains(message.Body, "\n") {
		// IRC lines cannot hold line breaks: send the whole body in a tag and a
		// one-line preview for clients that do not read it.
		tags += ";" + protocol.BodyTag + "=" + protocol.EncodeBody(message.Body)
	}
	return tags + " " + fmt.Sprintf(":%s!%s@localhost PRIVMSG %s :%s\r\n", message.From, username, message.Target, protocol.Preview(message.Body))
}
