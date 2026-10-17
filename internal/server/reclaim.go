package server

import (
	"crypto/subtle"
	"fmt"

	"github.com/Someblueman/airc/internal/protocol"
)

// A persistent session that lost its link silently stays registered until the
// server notices, up to ReadTimeout, and holds its nickname all that time. A
// client that noticed the loss first and redials would be refused with 433
// until then. Reclaim lets the new connection take the stale session over:
// a session authenticated as the same account, or one presenting the secret
// the stale session was given at registration (767), takes its nickname,
// channels and observations, and the stale session is closed without the
// QUIT and offline notices a real departure sends. Lines the stale session
// had queued are not replayed; the client catches up by message ID.

// resumeLocked records the nickname and secret a connection asks to take
// over, before it registers.
func (s *Server) resumeLocked(client *session, command protocol.Command) {
	if client.registered {
		s.numericLocked(client, "462", nil, "RESUME must be sent before registration")
		return
	}
	nick, _ := command.Param(0)
	secret, _ := command.Param(1)
	if !protocol.ValidNick(nick) || !protocol.ValidMessageID(secret) {
		s.numericLocked(client, "461", []string{"RESUME"}, "RESUME needs a nickname and its secret")
		return
	}
	client.resumeNick, client.resumeSecret = nickKey(nick), secret
}

// reclaimableLocked reports whether client may take over holder, the
// connected session holding the nickname client asked for. Ephemeral
// sessions never hold a nickname, so they never reclaim or are reclaimed.
func (s *Server) reclaimableLocked(client, holder *session, key string) bool {
	if holder == nil || holder == client || client.ephemeral || holder.ephemeral || holder.reclaimed {
		return false
	}
	if client.accountID != "" && holder.accountID == client.accountID && holder.authNick == key {
		return true
	}
	if client.resumeSecret == "" || client.resumeNick != key || holder.secret == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(client.resumeSecret), []byte(holder.secret)) == 1
}

// takeOverLocked moves holder's nickname, channels and observations to
// client, which is about to register, and closes holder. Nothing is
// broadcast: to the rooms, the nickname never left.
func (s *Server) takeOverLocked(client, holder *session) {
	key := nickKey(client.client.Nick)
	holder.reclaimed, holder.secret = true, ""
	if client.channels == nil {
		client.channels = map[string]struct{}{}
	}
	for channel := range holder.channels {
		client.channels[channel] = struct{}{}
		delete(s.channels[channel], holder.client.ID)
		s.channels[channel][client.client.ID] = client
	}
	if client.watching == nil {
		client.watching = map[string]struct{}{}
	}
	for target := range holder.watching {
		client.watching[target] = struct{}{}
		delete(s.watchers[target], holder.client.ID)
		s.watchers[target][client.client.ID] = client
	}
	if holder.observer && !client.ephemeral {
		client.observer = true
	}
	if client.away == "" {
		client.away = holder.away
	}
	holder.channels, holder.watching = nil, nil
	s.nicks[key] = client
	s.logger.Info("session_reclaimed", "nick", client.client.Nick, "old_id", holder.client.ID, "new_id", client.client.ID, "account_id", client.accountID)
	s.sendLocked(holder, "ERROR :Closing link: session reclaimed by a new connection\r\n")
	holder.closeAfterFlush()
}

// announceReclaimedLocked shows the registered client what it took over, as
// if it had joined each channel: the JOIN echo, names and topic.
func (s *Server) announceReclaimedLocked(client *session) {
	for channel := range client.channels {
		s.sendLocked(client, fmt.Sprintf(":%s!%s@localhost JOIN %s\r\n", client.client.Nick, client.client.Username, channel))
		s.namesOneLocked(client, channel)
		if _, ok := s.topics[channel]; ok {
			s.topicReplyLocked(client, channel)
		}
	}
	for target := range client.watching {
		s.numericLocked(client, "765", []string{target}, "Now observing")
	}
}
