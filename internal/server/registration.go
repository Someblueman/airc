package server

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/internal/version"
)

// ephemeralLocked marks a connection as a one-shot agent session. It must
// arrive before registration. Such a session does not claim its nickname, never
// appears in presence listings, may send to any channel without joining it, and
// may read history, so a short-lived command leaves no trace on the room.
func (s *Server) ephemeralLocked(client *session) {
	if client.registered {
		s.numericLocked(client, "462", nil, "EPHEMERAL must be sent before registration")
		return
	}
	client.ephemeral = true
}

func (s *Server) nickLocked(client *session, command protocol.Command) {
	nick, ok := command.Param(0)
	if !ok || !protocol.ValidNick(nick) {
		s.numericLocked(client, "432", []string{nick}, "Erroneous nickname")
		return
	}
	if rule, banned := s.restrictionLocked("ban", nick, "*"); banned {
		s.numericLocked(client, "465", []string{nick}, restrictionText("Banned", rule))
		return
	}
	if (!client.capNegotiating || client.registered) && !s.identityAllowedLocked(client, nick) {
		return
	}
	if client.ephemeral {
		if client.registered {
			s.numericLocked(client, "484", nil, "Ephemeral sessions cannot change nickname")
			return
		}
		client.client.Nick = nick
		s.tryRegisterLocked(client)
		return
	}
	key := nickKey(nick)
	if existing := s.nicks[key]; existing != nil && existing != client {
		// A connection still negotiating CAP may authenticate after NICK,
		// so whether it can reclaim the holder is decided at registration.
		if client.registered || !client.capNegotiating && !s.reclaimableLocked(client, existing, key) {
			s.numericLocked(client, "433", []string{nick}, "Nickname is already in use")
			return
		}
		// The holder keeps its claim until this connection registers and
		// takes the session over; see reclaim.go.
		if old := client.client.Nick; old != "" && s.nicks[nickKey(old)] == client {
			delete(s.nicks, nickKey(old))
		}
		client.client.Nick = nick
		s.tryRegisterLocked(client)
		return
	}
	old := client.client.Nick
	if client.registered && !client.hidden() && !strings.EqualFold(old, nick) {
		s.monitorChangedLocked(client, false)
	}
	if client.registered {
		s.broadcastClientLocked(client, fmt.Sprintf(":%s!%s@localhost NICK :%s\r\n", old, client.client.Username, nick))
	}
	if old != "" && s.nicks[nickKey(old)] == client {
		// Release the previous claim, including before registration completes.
		delete(s.nicks, nickKey(old))
	}
	client.client.Nick = nick
	s.nicks[key] = client
	if client.registered && !client.hidden() && !strings.EqualFold(old, nick) {
		s.monitorChangedLocked(client, true)
	}
	if !client.registered {
		s.tryRegisterLocked(client)
	}
}

func (s *Server) userLocked(client *session, command protocol.Command) {
	if client.registered {
		s.numericLocked(client, "462", nil, "You may not reregister")
		return
	}
	username, ok := command.Param(0)
	if !ok || username == "" || strings.ContainsAny(username, " \r\n\x00") {
		s.numericLocked(client, "461", []string{"USER"}, "Not enough parameters")
		return
	}
	client.client.Username = username
	client.client.RealName = command.Trailing
	if client.client.RealName == "" && len(command.Params) > 3 {
		client.client.RealName = strings.Join(command.Params[3:], " ")
	}
	if len(client.client.Username) > 32 || len(client.client.RealName) > 256 || !utf8.ValidString(client.client.Username) || !utf8.ValidString(client.client.RealName) {
		client.client.Username = ""
		client.client.RealName = ""
		s.numericLocked(client, "417", nil, "User details exceed server limits")
		return
	}
	s.tryRegisterLocked(client)
}

func (s *Server) tryRegisterLocked(client *session) {
	if client.capNegotiating || client.registered || client.client.Nick == "" || client.client.Username == "" {
		return
	}
	key := nickKey(client.client.Nick)
	if holder := s.nicks[key]; !client.ephemeral && holder != nil && holder != client && !s.reclaimableLocked(client, holder, key) {
		// Refused at NICK unless the connection was still negotiating CAP
		// (see nickLocked); the nickname is in use either way.
		s.numericLocked(client, "433", []string{client.client.Nick}, "Nickname is already in use")
		client.client.Nick = ""
		return
	}
	if !s.identityAllowedLocked(client, client.client.Nick) {
		return
	}
	reclaimed := false
	if client.ephemeral && client.accountID == "" {
		// Ephemeral sessions do not claim their nickname, so they may share it
		// with each other, but a guest cannot speak as a nickname a connected
		// client holds: as in IRC, the name is in use.
		if holder := s.nicks[key]; holder != nil && holder != client {
			s.numericLocked(client, "433", []string{client.client.Nick}, "Nickname is already in use")
			client.client.Nick = ""
			return
		}
	}
	if !client.ephemeral {
		switch holder := s.nicks[key]; {
		case holder == nil:
			s.nicks[key] = client // the claim lapsed since NICK; see nickLocked
		case holder == client:
		default: // reclaimable, checked above
			s.takeOverLocked(client, holder)
			reclaimed = true
		}
		client.secret = newID()
	}
	client.registered = true
	s.releasePendingLocked(client)
	if !client.hidden() && !reclaimed {
		s.monitorChangedLocked(client, true)
	}
	s.logger.Info("client_registered", "id", client.client.ID, "nick", client.client.Nick, "ephemeral", client.ephemeral)
	if client.ephemeral {
		// Sent before the welcome so a client knows the mode once registration completes.
		s.numericLocked(client, "766", nil, "Ephemeral session")
	}
	// Advertised before the welcome so a client knows the features once registered.
	features := []string{"RESUME=1", "CHECK=1", "RECEIPTS=1", "BOT_REPLIES=1", "MONITOR=128", "AWAYLEN=240", "PREFIX=(o)@", "CHANMODES=,,,", "CHANNEL_OPERATORS=1", "MULTILINE=1", "MENTIONS=1", "DM_AUDIT=1", "REPLIES=1", "REACTIONS=1", "DIRECTORY=1", "SEARCH=1", "SEARCH_TERMS=1", "TOPIC=1", "CHANNELS=1", "HISTORY_START=1", fmt.Sprintf("HISTORY=%d", s.cfg.HistoryLimit), "STATUS=1", "RUN=1", "SERVER_VERSION=" + version.String()}
	if s.accessEnabled {
		features = append(features, "ACCESS=1")
	}
	if s.gatewayEnabled {
		features = append(features, "WEBIRC=1")
	}
	if s.cfg.TLSConfig != nil {
		features = append(features, "TLS=1")
	}
	if len(s.cfg.Carriers) > 0 {
		features = append(features, "CARRIERS="+protocol.FormatCarriers(s.cfg.Carriers))
	}
	if s.adminEnabled {
		features = append(features, "ADMIN=1")
	}
	if s.accountsAt != "" {
		features = append(features, "ACCOUNTS=1")
	}
	if s.wake != nil {
		features = append(features, "WAKE=1")
	}
	features = append(features, chatFeatures...)
	features = append(features, fmt.Sprintf("PASTE_BYTES=%d", s.cfg.PasteBytes))
	if s.cfg.HistoryLimit > 0 {
		features = append(features, "IDEMPOTENCY=1", "SAFE_RETRY=1")
	}
	s.numericLocked(client, "005", features, "are supported by this server")
	if client.secret != "" {
		// Before the welcome, so a client has it once registration completes.
		s.numericLocked(client, "767", []string{client.secret}, "Resume secret")
	}
	s.numericLocked(client, "001", nil, "Welcome to airc, "+client.client.Nick)
	s.numericLocked(client, "002", nil, "Your host is airc, running version 1")
	s.numericLocked(client, "003", nil, "This server was created for local agent communication")
	s.numericLocked(client, "004", []string{"airc", "1", "it"}, "")
	s.numericLocked(client, "422", nil, "MOTD file is missing")
	if reclaimed {
		s.announceReclaimedLocked(client)
	}
}
