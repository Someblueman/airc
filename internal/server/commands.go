package server

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/internal/version"
)

func (s *Server) handle(client *session, command protocol.Command) {
	if command.Name == "PRIVMSG" || command.Name == "NOTICE" || command.Name == "REPLY" || command.Name == "REACT" || command.Name == "ADMIN" || command.Name == "CHAT" {
		s.messageMu.Lock()
		defer s.messageMu.Unlock()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing.Load() {
		return
	}
	if _, connected := s.clients[client.client.ID]; !connected {
		return
	}
	select {
	case <-client.done:
		return
	default:
	}
	if command.Name == "PASS" {
		s.passLocked(client, command)
		return
	}
	if s.accessEnabled && !client.access && command.Name != "QUIT" {
		s.numericLocked(client, "464", nil, "Connection credential required")
		return
	}
	if command.Name == "CAP" {
		s.capLocked(client, command)
		return
	}
	if command.Name == "AUTHENTICATE" {
		s.authenticateLocked(client, command)
		return
	}
	if !client.registered && command.Name != "NICK" && command.Name != "USER" && command.Name != "PING" && command.Name != "PONG" && command.Name != "QUIT" && command.Name != "EPHEMERAL" && command.Name != "AUTH" && command.Name != "REGISTER" {
		s.numericLocked(client, "451", nil, "You have not registered")
		return
	}
	if client.observer && command.Name != "OBSERVE" && command.Name != "UNOBSERVE" && command.Name != "PING" && command.Name != "PONG" && command.Name != "QUIT" {
		s.numericLocked(client, "484", nil, "Observer connections are read-only")
		return
	}
	if client.registered {
		s.touchCardLocked(client.client.Nick)
	}
	if (command.Name == "PROFILE" || command.Name == "PRESENCE") && !s.postAllowedLocked(client, "*") {
		return
	}
	switch command.Name {
	case "MODE":
		s.modeLocked(client, command)
	case "KICK":
		s.kickLocked(client, command)
	case "AWAY":
		s.awayLocked(client, command)
	case "MONITOR":
		s.monitorLocked(client, command)
	case "CHAT":
		s.chatLocked(client, command)
	case "AUTH", "REGISTER":
		s.authLocked(client, command)
	case "OPER":
		s.operLocked(client, command)
	case "ADMIN":
		s.adminLocked(client, command)
	case "NICK":
		s.nickLocked(client, command)
	case "USER":
		s.userLocked(client, command)
	case "JOIN":
		s.joinLocked(client, command)
	case "EPHEMERAL":
		s.ephemeralLocked(client)
	case "OBSERVE":
		s.observeLocked(client, command)
	case "UNOBSERVE":
		s.unobserveLocked(client, command)
	case "PART":
		s.partLocked(client, command)
	case "PRIVMSG":
		s.messageLocked(client, command, false, nil)
	case "NOTICE":
		s.messageLocked(client, command, true, nil)
	case "REPLY", "REACT":
		s.replyLocked(client, command)
	case "DIRECTORY":
		s.directoryLocked(client, command)
	case "PROFILE":
		s.profileLocked(client, command)
	case "PRESENCE":
		s.presenceLocked(client, command)
	case "SEARCH":
		s.searchLocked(client, command)
	case "QUIT":
		reason := command.Trailing
		if reason == "" {
			reason = "Client Quit"
		}
		client.quitReason = reason
		client.close()
	case "PING":
		token := command.Trailing
		if token == "" {
			token, _ = command.Param(0)
		}
		s.sendLocked(client, fmt.Sprintf(":server PONG server :%s\r\n", token))
	case "PONG":
		client.lastPong.Store(time.Now().UnixNano())
	case "WHO":
		s.whoLocked(client, command)
	case "WHOIS":
		s.whoisLocked(client, command)
	case "NAMES":
		s.namesLocked(client, command)
	case "LIST":
		s.listLocked(client)
	case "TOPIC":
		s.topicLocked(client, command)
	case "CHANNELS":
		s.channelsLocked(client)
	case "CHECK":
		s.checkLocked(client, command)
	case "RETRY":
		s.retryRequestLocked(client, command)
	case "HISTORY":
		s.historyLocked(client, command)
	case "AGENTS":
		s.agentsLocked(client)
	case "STATUS":
		s.statusLocked(client)
	default:
		s.numericLocked(client, "421", []string{command.Name}, "Unknown command")
	}
}

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

func (s *Server) observeLocked(client *session, command protocol.Command) {
	list, ok := command.Param(0)
	if !ok {
		s.numericLocked(client, "461", []string{"OBSERVE"}, "Not enough parameters")
		return
	}
	if len(client.channels) > 0 {
		s.numericLocked(client, "484", nil, "Leave joined channels before observing")
		return
	}
	requested := strings.Split(list, ",")
	if len(requested) > 16 {
		s.numericLocked(client, "407", nil, "Too many targets")
		return
	}
	for _, target := range requested {
		// "@nick" observes the direct messages addressed to a nickname.
		key := target
		switch {
		case target == protocol.AllDirectMessages:
		case strings.HasPrefix(target, "thread:") || strings.HasPrefix(target, "replies:"):
			var err error
			key, err = s.history.conversation(target)
			if err != nil {
				s.numericLocked(client, "430", []string{target}, err.Error())
				continue
			}
		case strings.HasPrefix(target, "@") && validNick(target[1:]):
			key = "@" + nickKey(target[1:])
		case !validChannel(target):
			s.numericLocked(client, "403", []string{target}, "No such channel")
			continue
		}
		if channel := s.conversationChannelLocked(key); isChannelName(channel) && !s.channelAllowedLocked(client, channel) {
			continue
		}
		if _, watching := client.watching[key]; watching {
			s.numericLocked(client, "765", []string{target}, "Now observing")
			continue
		}
		if len(client.watching) >= maxChannelsPerClient {
			s.numericLocked(client, "405", []string{target}, "You are observing too many channels")
			continue
		}
		if s.watchers[key] == nil {
			if len(s.watchers) >= maxTotalChannels {
				s.numericLocked(client, "437", []string{target}, "Server observation limit reached")
				continue
			}
			s.watchers[key] = make(map[string]*session)
		}
		if !client.ephemeral {
			// Ephemeral sessions stay fully usable (history, send) while observing.
			if !client.observer {
				s.monitorChangedLocked(client, false)
			}
			client.observer = true
		}
		client.watching[key] = struct{}{}
		s.watchers[key][client.client.ID] = client
		s.logger.Info("channel_observed", "nick", client.client.Nick, "channel", key)
		s.numericLocked(client, "765", []string{target}, "Now observing")
	}
}

func (s *Server) nickLocked(client *session, command protocol.Command) {
	nick, ok := command.Param(0)
	if !ok || !validNick(nick) {
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
		s.numericLocked(client, "433", []string{nick}, "Nickname is already in use")
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
	if !s.identityAllowedLocked(client, client.client.Nick) {
		return
	}
	client.registered = true
	if !client.hidden() {
		s.monitorChangedLocked(client, true)
	}
	s.logger.Info("client_registered", "id", client.client.ID, "nick", client.client.Nick, "ephemeral", client.ephemeral)
	if client.ephemeral {
		// Sent before the welcome so a client knows the mode once registration completes.
		s.numericLocked(client, "766", nil, "Ephemeral session")
	}
	// Advertised before the welcome so a client knows the features once registered.
	features := []string{"CHECK=1", "RECEIPTS=1", "BOT_REPLIES=1", "MONITOR=128", "AWAYLEN=240", "PREFIX=(o)@", "CHANMODES=,,,", "CHANNEL_OPERATORS=1", "MULTILINE=1", "MENTIONS=1", "DM_AUDIT=1", "REPLIES=1", "REACTIONS=1", "DIRECTORY=1", "SEARCH=1", "TOPIC=1", "CHANNELS=1", "HISTORY_START=1", fmt.Sprintf("HISTORY=%d", s.cfg.HistoryLimit), "STATUS=1", "SERVER_VERSION=" + version.String()}
	if s.accessEnabled {
		features = append(features, "ACCESS=1")
	}
	if s.cfg.TLSConfig != nil {
		features = append(features, "TLS=1")
	}
	if s.adminEnabled {
		features = append(features, "ADMIN=1")
	}
	if s.accountsAt != "" {
		features = append(features, "ACCOUNTS=1")
	}
	features = append(features, "CHAT=1", "CUSTOM_REACTIONS=1")
	if s.cfg.HistoryLimit > 0 {
		features = append(features, "IDEMPOTENCY=1", "SAFE_RETRY=1")
	}
	s.numericLocked(client, "005", features, "are supported by this server")
	s.numericLocked(client, "001", nil, "Welcome to airc, "+client.client.Nick)
	s.numericLocked(client, "002", nil, "Your host is airc, running version 1")
	s.numericLocked(client, "003", nil, "This server was created for local agent communication")
	s.numericLocked(client, "004", []string{"airc", "1", "it"}, "")
	s.numericLocked(client, "422", nil, "MOTD file is missing")
}

func (s *Server) joinLocked(client *session, command protocol.Command) {
	if client.ephemeral {
		s.numericLocked(client, "484", nil, "Ephemeral sessions cannot join channels")
		return
	}
	list, ok := command.Param(0)
	if !ok {
		s.numericLocked(client, "461", []string{"JOIN"}, "Not enough parameters")
		return
	}
	if list == "0" {
		for channel := range client.channels {
			s.partOneLocked(client, channel, "")
		}
		return
	}
	requested := strings.Split(list, ",")
	if len(requested) > 16 {
		s.numericLocked(client, "407", nil, "Too many targets")
		return
	}
	for _, channel := range requested {
		if !validChannel(channel) {
			s.numericLocked(client, "403", []string{channel}, "No such channel")
			continue
		}
		if !s.channelAllowedLocked(client, channel) {
			continue
		}
		if _, joined := client.channels[channel]; joined {
			continue
		}
		if len(client.channels) >= maxChannelsPerClient {
			s.numericLocked(client, "405", []string{channel}, "You have joined too many channels")
			continue
		}
		if s.channels[channel] == nil {
			if len(s.channels) >= maxTotalChannels {
				s.numericLocked(client, "437", []string{channel}, "Server channel limit reached")
				continue
			}
			s.channels[channel] = make(map[string]*session)
		}
		s.channels[channel][client.client.ID] = client
		client.channels[channel] = struct{}{}
		s.broadcastChannelLocked(channel, fmt.Sprintf(":%s!%s@localhost JOIN %s\r\n", client.client.Nick, client.client.Username, channel))
		s.logger.Info("channel_join", "nick", client.client.Nick, "channel", channel)
		s.namesOneLocked(client, channel)
		if _, ok := s.topics[channel]; ok {
			s.topicReplyLocked(client, channel)
		}
	}
}

func (s *Server) partLocked(client *session, command protocol.Command) {
	list, ok := command.Param(0)
	if !ok {
		s.numericLocked(client, "461", []string{"PART"}, "Not enough parameters")
		return
	}
	channels := strings.Split(list, ",")
	if len(channels) > 16 {
		s.numericLocked(client, "407", nil, "Too many targets")
		return
	}
	for _, channel := range channels {
		s.partOneLocked(client, channel, command.Trailing)
	}
}

func (s *Server) partOneLocked(client *session, channel, reason string) {
	if _, joined := client.channels[channel]; !joined {
		s.numericLocked(client, "442", []string{channel}, "You're not on that channel")
		return
	}
	s.broadcastChannelLocked(channel, fmt.Sprintf(":%s!%s@localhost PART %s :%s\r\n", client.client.Nick, client.client.Username, channel, reason))
	delete(s.channels[channel], client.client.ID)
	delete(client.channels, channel)
	if len(s.channels[channel]) == 0 {
		delete(s.channels, channel)
	}
	s.logger.Info("channel_part", "nick", client.client.Nick, "channel", channel)
}

func (s *Server) broadcastClientLocked(client *session, line string) {
	seen := make(map[string]struct{})
	sentSelf := false
	for channel := range client.channels {
		for id, member := range s.channels[channel] {
			if _, ok := seen[id]; !ok {
				member.enqueue(line)
				seen[id] = struct{}{}
				if id == client.client.ID {
					sentSelf = true
				}
			}
		}
	}
	if !sentSelf {
		client.enqueue(line)
	}
}

func (s *Server) broadcastChannelLocked(channel, line string) {
	for _, member := range s.channels[channel] {
		if _, banned := s.restrictionLocked("ban", member.client.Nick, channel); banned {
			continue
		}
		member.enqueue(line)
	}
	s.broadcastWatchersLocked(channel, line)
}

// broadcastWatchersLocked delivers line to observers of a channel or, for keys
// of the form "@nick", of a nickname's direct messages.
func (s *Server) broadcastWatchersLocked(key, line string) {
	for _, watcher := range s.watchers[key] {
		if isChannelName(key) {
			if _, banned := s.restrictionLocked("ban", watcher.client.Nick, key); banned {
				continue
			}
		}
		watcher.enqueue(line)
	}
}

func (s *Server) sendLocked(client *session, line string) { client.enqueue(line) }

func (s *Server) numeric(client *session, code string, params []string, trailing string) {
	s.mu.Lock()
	s.numericLocked(client, code, params, trailing)
	s.mu.Unlock()
}

func (s *Server) numericLocked(client *session, code string, params []string, trailing string) {
	if client.silentNotice && (code >= "400" && code < "600" || code == "762") {
		return
	}
	all := make([]string, 0, len(params)+1)
	nick := client.client.Nick
	if nick == "" {
		nick = "*"
	}
	all = append(all, nick)
	all = append(all, params...)
	line := protocol.Format("server", code, all, trailing)
	if !strings.HasSuffix(line, "\r\n") {
		line = ":server ERROR :response too long\r\n"
	}
	client.enqueue(line)
}
