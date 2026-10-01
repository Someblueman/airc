package server

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
)

const maxDirectoryCards = 1024

func (s *Server) directoryCard(nick string, now time.Time) protocol.AgentCard {
	card := s.directory[nickKey(nick)]
	if a, ok := s.accounts[nickKey(nick)]; ok {
		card.AccountID = a.ID
	}
	card.Connected = false
	if card.Nick == "" {
		card.Nick = nick
	}
	if card.State == "" || !now.Before(card.ExpiresAt) {
		card.State, card.Note = "unknown", ""
	}
	if live := s.nicks[nickKey(nick)]; live != nil && !live.hidden() {
		card.Connected = true
		if card.LastSeen.IsZero() {
			card.LastSeen = live.client.ConnectedAt
		}
	}
	return card
}

func (s *Server) directoryLocked(client *session, command protocol.Command) {
	nick, _ := command.Param(0)
	if nick != "" && !validNick(nick) {
		s.numericLocked(client, "461", nil, "DIRECTORY requires a nickname or no target")
		return
	}
	names := map[string]string{}
	if nick != "" {
		names[nickKey(nick)] = nick
	} else {
		for key, a := range s.accounts {
			names[key] = a.Nick
		}
		for key, card := range s.directory {
			names[key] = card.Nick
		}
		for key, live := range s.nicks {
			if !live.hidden() {
				names[key] = live.client.Nick
			}
		}
	}
	keys := make([]string, 0, len(names))
	for key := range names {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	now := time.Now().UTC()
	for _, key := range keys {
		s.cardReplyLocked(client, s.directoryCard(names[key], now))
	}
	s.numericLocked(client, "774", nil, "End of directory")
}

func (s *Server) cardReplyLocked(client *session, card protocol.AgentCard) {
	data, _ := json.Marshal(card)
	s.numericLocked(client, "773", nil, string(data))
}

func (s *Server) roomForCardLocked(key string, now time.Time) bool {
	if _, exists := s.directory[key]; exists {
		return true
	}
	// Expired presence without a profile is disposable; profiles stay until cleared.
	for other, card := range s.directory {
		if card.AgentProfile == (protocol.AgentProfile{}) && !now.Before(card.ExpiresAt) {
			delete(s.directory, other)
		}
	}
	return len(s.directory) < maxDirectoryCards
}

func (s *Server) presenceLocked(client *session, command protocol.Command) {
	state, _ := command.Param(0)
	ttl, _ := command.Param(1)
	seconds, err := strconv.Atoi(ttl)
	if state != "clear" && (!protocol.ValidPresence(state) || err != nil || seconds < 1 || seconds > 3600) || !protocol.BriefText(command.Trailing, 240) {
		s.numericLocked(client, "461", nil, "PRESENCE requires available/thinking/running/away and a TTL of 1-3600 seconds; note at most 240 bytes")
		return
	}
	now, key := time.Now().UTC(), nickKey(client.client.Nick)
	if !s.roomForCardLocked(key, now) {
		s.numericLocked(client, "437", nil, "Directory is full; clear unused profiles")
		return
	}
	card := s.directoryCard(client.client.Nick, now)
	card.Nick, card.State, card.Note, card.LastSeen = client.client.Nick, state, strings.TrimSpace(command.Trailing), now
	card.ExpiresAt = now.Add(time.Duration(seconds) * time.Second)
	if state == "clear" {
		card.State, card.Note, card.ExpiresAt = "unknown", "", time.Time{}
	}
	s.directory[key] = card
	if state == "clear" && card.AgentProfile == (protocol.AgentProfile{}) {
		delete(s.directory, key)
	}
	s.cardReplyLocked(client, card)
	s.numericLocked(client, "774", nil, "Presence updated")
}

func (s *Server) profileLocked(client *session, command protocol.Command) {
	var patch map[string]string
	if len(command.Trailing) > 4096 || json.Unmarshal([]byte(command.Trailing), &patch) != nil || len(patch) == 0 {
		s.numericLocked(client, "461", nil, "PROFILE requires a JSON object of model/workspace/tools/about, or clear")
		return
	}
	now, key := time.Now().UTC(), nickKey(client.client.Nick)
	card := s.directoryCard(client.client.Nick, now)
	for field, value := range patch {
		if !protocol.BriefText(value, 400) {
			s.numericLocked(client, "417", nil, "Profile fields must be text of at most 400 bytes")
			return
		}
		switch field {
		case "model":
			card.Model = value
		case "workspace":
			card.Workspace = value
		case "tools":
			card.Tools = value
		case "about":
			card.About = value
		case "clear":
			if len(patch) != 1 || value != "true" {
				s.numericLocked(client, "461", nil, "Profile clear cannot be combined with fields")
				return
			}
			card.AgentProfile = protocol.AgentProfile{}
		default:
			s.numericLocked(client, "461", nil, "Unknown profile field")
			return
		}
	}
	if !s.roomForCardLocked(key, now) {
		s.numericLocked(client, "437", nil, "Directory is full; clear unused profiles")
		return
	}
	card.Nick, card.LastSeen = client.client.Nick, now
	if err := s.saveProfileLocked(key, card); err != nil {
		s.numericLocked(client, "437", nil, "Profile was not saved: "+err.Error())
		return
	}
	s.directory[key] = card
	if card.AgentProfile == (protocol.AgentProfile{}) && !now.Before(card.ExpiresAt) {
		delete(s.directory, key)
	}
	s.cardReplyLocked(client, card)
	s.numericLocked(client, "774", nil, "Profile updated")
}
