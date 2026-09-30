package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
	if body == "" {
		if !notice {
			s.numericLocked(client, "412", nil, "No text to send")
		}
		return
	}
	if len(body) > s.cfg.MaxMessageSize {
		if !notice {
			s.numericLocked(client, "417", nil, "Message is too long")
		}
		return
	}
	for _, target := range strings.Split(targets, ",") {
		if strings.HasPrefix(target, "#") || strings.HasPrefix(target, "&") {
			members := s.channels[target]
			if members == nil {
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
			message := s.newMessage(client.client.Nick, target, body)
			s.history.add(message)
			s.broadcastChannelLocked(target, formatMessage(message, client.client.Username))
			s.logger.Info("message_sent", "id", message.ID, "from", message.From, "target", target)
			continue
		}
		recipient := s.nicks[nickKey(target)]
		if recipient == nil {
			if !notice {
				s.numericLocked(client, "401", []string{target}, "No such nick")
			}
			continue
		}
		message := s.newMessage(client.client.Nick, recipient.client.Nick, body)
		s.history.add(message)
		recipient.enqueue(formatMessage(message, client.client.Username))
		encoded, _ := json.Marshal(message)
		s.numericLocked(client, "762", []string{recipient.client.Nick}, string(encoded))
		s.logger.Info("message_sent", "id", message.ID, "from", message.From, "target", recipient.client.Nick)
	}
}

func (s *Server) newMessage(from, target, body string) Message {
	return Message{ID: newID(), From: from, Target: target, Body: body, Timestamp: time.Now().UTC()}
}

func formatMessage(message Message, username string) string {
	tags := "@msgid=" + message.ID + ";time=" + protocol.EscapeTag(message.Timestamp.Format(time.RFC3339Nano)) + " "
	return tags + fmt.Sprintf(":%s!%s@localhost PRIVMSG %s :%s\r\n", message.From, username, message.Target, message.Body)
}
