package server

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/protocol"
)

func (s *Server) messageLocked(client *session, command protocol.Command, notice bool) {
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
			message := s.newMessage(client.client.Nick, target, body)
			s.recordLocked(message)
			s.broadcastChannelLocked(target, formatMessage(message, client.client.Username))
			if client.ephemeral {
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
		message := s.newMessage(client.client.Nick, to, body)
		s.recordLocked(message)
		line := formatMessage(message, client.client.Username)
		if live {
			recipient.enqueue(line)
		}
		s.broadcastWatchersLocked("@"+nickKey(to), line)
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
	return protocol.EncodeMessageMetadata(protocol.MessageMetadata{ID: message.ID, Seq: message.Seq, From: message.From, Target: message.Target, Message: message.Body, Timestamp: message.Timestamp})
}

func (s *Server) newMessage(from, target, body string) Message {
	s.seq++
	return Message{ID: newID(), Seq: s.seq, From: from, Target: target, Body: body, Timestamp: time.Now().UTC()}
}

func formatMessage(message Message, username string) string {
	tags := "@msgid=" + message.ID + ";time=" + protocol.EscapeTag(message.Timestamp.Format(time.RFC3339Nano))
	if strings.Contains(message.Body, "\n") {
		// IRC lines cannot hold line breaks: send the whole body in a tag and a
		// one-line preview for clients that do not read it.
		tags += ";" + protocol.BodyTag + "=" + protocol.EncodeBody(message.Body)
	}
	return tags + " " + fmt.Sprintf(":%s!%s@localhost PRIVMSG %s :%s\r\n", message.From, username, message.Target, protocol.Preview(message.Body))
}
