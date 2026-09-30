package server

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/protocol"
)

func (s *Server) handle(client *session, command protocol.Command) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, connected := s.clients[client.client.ID]; !connected {
		return
	}
	if !client.registered && command.Name != "NICK" && command.Name != "USER" && command.Name != "PING" && command.Name != "PONG" && command.Name != "QUIT" {
		s.numericLocked(client, "451", nil, "You have not registered")
		return
	}
	switch command.Name {
	case "NICK":
		s.nickLocked(client, command)
	case "USER":
		s.userLocked(client, command)
	case "JOIN":
		s.joinLocked(client, command)
	case "PART":
		s.partLocked(client, command)
	case "PRIVMSG":
		s.messageLocked(client, command, false)
	case "NOTICE":
		s.messageLocked(client, command, true)
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
	case "HISTORY":
		s.historyLocked(client, command)
	case "AGENTS":
		s.agentsLocked(client)
	default:
		s.numericLocked(client, "421", []string{command.Name}, "Unknown command")
	}
}

func (s *Server) nickLocked(client *session, command protocol.Command) {
	nick, ok := command.Param(0)
	if !ok || !validNick(nick) {
		s.numericLocked(client, "432", []string{nick}, "Erroneous nickname")
		return
	}
	key := nickKey(nick)
	if existing := s.nicks[key]; existing != nil && existing != client {
		s.numericLocked(client, "433", []string{nick}, "Nickname is already in use")
		return
	}
	old := client.client.Nick
	if client.registered {
		s.broadcastClientLocked(client, fmt.Sprintf(":%s!%s@localhost NICK :%s\r\n", old, client.client.Username, nick))
		delete(s.nicks, nickKey(old))
	}
	client.client.Nick = nick
	s.nicks[key] = client
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
	if client.registered || client.client.Nick == "" || client.client.Username == "" {
		return
	}
	client.registered = true
	s.logger.Info("client_registered", "id", client.client.ID, "nick", client.client.Nick)
	s.numericLocked(client, "001", nil, "Welcome to airc, "+client.client.Nick)
	s.numericLocked(client, "002", nil, "Your host is airc, running version 1")
	s.numericLocked(client, "003", nil, "This server was created for local agent communication")
	s.numericLocked(client, "004", []string{"airc", "1", "it"}, "")
	s.numericLocked(client, "422", nil, "MOTD file is missing")
}

func (s *Server) joinLocked(client *session, command protocol.Command) {
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
		member.enqueue(line)
	}
}

func (s *Server) sendLocked(client *session, line string) { client.enqueue(line) }

func (s *Server) numeric(client *session, code string, params []string, trailing string) {
	s.mu.Lock()
	s.numericLocked(client, code, params, trailing)
	s.mu.Unlock()
}

func (s *Server) numericLocked(client *session, code string, params []string, trailing string) {
	all := make([]string, 0, len(params)+1)
	all = append(all, client.client.Nick)
	all = append(all, params...)
	line := protocol.Format("server", code, all, trailing)
	if !strings.HasSuffix(line, "\r\n") {
		line = ":server ERROR :response too long\r\n"
	}
	client.enqueue(line)
}
