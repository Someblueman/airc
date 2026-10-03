package server

import (
	"strings"

	"github.com/Someblueman/airc/internal/protocol"
)

func requestKey(nick, accountID, requestID string) string {
	if requestID == "" {
		return ""
	}
	if accountID != "" {
		return accountID + ":" + requestID
	}
	return "guest:" + nickKey(nick) + ":" + requestID
}

// RETRY is a lookup only. A missing/evicted request never creates a new post.
func (s *Server) retryRequestLocked(client *session, command protocol.Command) {
	id, _ := command.Param(0)
	if !protocol.ValidRequestID(id) || s.cfg.HistoryLimit == 0 {
		s.numericLocked(client, "461", nil, "RETRY requires history and a valid request ID")
		return
	}
	index, found := s.history.requests[requestKey(client.client.Nick, client.accountID, id)]
	if !found {
		s.numericLocked(client, "488", nil, "request is not retained; delivery is unknown; inspect history before deciding to send again")
		return
	}
	m := s.history.items[index]
	s.receiptLocked(client, m, !isChannelName(m.Target) && s.liveNickLocked(m.Target) == nil)
}

func (s *Server) retryLocked(client *session, command protocol.Command, target, body string, parent *Message) bool {
	id := command.Tags[protocol.RequestIDTag]
	if id == "" {
		return false
	}
	if !protocol.ValidRequestID(id) || s.cfg.HistoryLimit == 0 || len(strings.Split(command.Params[0], ",")) != 1 || command.Name == "NOTICE" {
		s.numericLocked(client, "461", nil, "request ID requires 1-64 letters/digits/-/_, history, and one message target")
		return true
	}
	index, found := s.history.requests[requestKey(client.client.Nick, client.accountID, id)]
	if !found {
		return false
	}
	old := s.history.items[index]
	replyTo, reaction := "", ""
	if parent != nil {
		replyTo = parent.ID
	}
	if command.Name == "REACT" {
		reaction = body
	}
	equalTarget := old.Target == target
	if !isChannelName(target) {
		equalTarget = strings.EqualFold(old.Target, target)
	}
	if !equalTarget || old.Body != body || old.ReplyTo != replyTo || old.Reaction != reaction {
		s.numericLocked(client, "487", nil, "request ID was already used for different content")
		return true
	}
	s.receiptLocked(client, old, !isChannelName(target) && s.liveNickLocked(target) == nil)
	return true
}
