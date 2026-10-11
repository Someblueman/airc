package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
)

// serializesPosts reports whether a command may append to history and so must
// hold messageMu, which a post keeps across its disk write. Reads and
// transient signals skip it so they never queue behind a burst of posts.
// Anything unrecognised is serialized.
func serializesPosts(command protocol.Command) bool {
	switch command.Name {
	case "PRIVMSG", "NOTICE", "REPLY", "REACT", "ADMIN":
		return true
	case "CHAT":
		var request struct {
			Action string `json:"action"`
		}
		if len(command.Trailing) > 7000 || json.Unmarshal([]byte(command.Trailing), &request) != nil {
			return true
		}
		return !chatReadOnlyActions[request.Action]
	}
	return false
}

// commandGate records which connection states may send a command. A command
// absent from commandGates is allowed in neither state.
type commandGate struct {
	preRegistration bool // accepted before NICK and USER complete registration
	observerOK      bool // accepted on a read-only observer connection
}

var commandGates = map[string]commandGate{
	"NICK":      {preRegistration: true},
	"USER":      {preRegistration: true},
	"PING":      {preRegistration: true, observerOK: true},
	"PONG":      {preRegistration: true, observerOK: true},
	"QUIT":      {preRegistration: true, observerOK: true},
	"EPHEMERAL": {preRegistration: true},
	"AUTH":      {preRegistration: true},
	"REGISTER":  {preRegistration: true},
	"OBSERVE":   {observerOK: true},
	"UNOBSERVE": {observerOK: true},
}

// maxTargets bounds the comma-separated target list of one command.
const maxTargets = 16

// targetListLocked splits a comma-separated target list. A list longer than
// maxTargets is answered with 407 and reported as not ok.
func (s *Server) targetListLocked(client *session, list string) ([]string, bool) {
	targets := strings.Split(list, ",")
	if len(targets) > maxTargets {
		s.numericLocked(client, "407", nil, "Too many targets")
		return nil, false
	}
	return targets, true
}

func (s *Server) handle(client *session, command protocol.Command) {
	if serializesPosts(command) {
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
	if client.finishing {
		return
	}
	if command.Name == "WEBIRC" && s.gatewayEnabled {
		s.webircLocked(client, command)
		return
	}
	client.settled = true
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
	gate := commandGates[command.Name]
	if !client.registered && !gate.preRegistration {
		s.numericLocked(client, "451", nil, "You have not registered")
		return
	}
	if client.observer && !gate.observerOK {
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
		s.quitLocked(client, command)
	case "PING":
		s.pingLocked(client, command)
	case "PONG":
		s.pongLocked(client)
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

func (s *Server) quitLocked(client *session, command protocol.Command) {
	reason := command.Trailing
	if reason == "" {
		reason = "Client Quit"
	}
	client.quitReason = reason
	client.close()
}

func (s *Server) pingLocked(client *session, command protocol.Command) {
	token := command.Trailing
	if token == "" {
		token, _ = command.Param(0)
	}
	s.sendLocked(client, fmt.Sprintf(":server PONG server :%s\r\n", token))
}

func (s *Server) pongLocked(client *session) {
	client.lastPong.Store(time.Now().UnixNano())
}
