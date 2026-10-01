package server

import (
	"encoding/json"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/internal/version"
)

type agentListing struct {
	Nick        string    `json:"nick"`
	Channels    []string  `json:"channels"`
	ConnectedAt time.Time `json:"connected_at"`
}

func (s *Server) statusLocked(client *session) {
	data, _ := json.Marshal(protocol.ServerStatus{
		Version: version.String(), PID: os.Getpid(), Connections: len(s.clients),
		MaxConnections: s.cfg.MaxConnections, HistoryLimit: s.cfg.HistoryLimit,
		HistorySize: s.history.size, HistoryFile: s.histFile != nil, PersistenceError: s.persistenceError,
	})
	s.numericLocked(client, "770", nil, string(data))
}

func (s *Server) whoLocked(client *session, command interface{ Param(int) (string, bool) }) {
	target, _ := command.Param(0)
	if strings.HasPrefix(target, "#") || strings.HasPrefix(target, "&") {
		for _, member := range s.channels[target] {
			if !member.hidden() {
				s.whoReplyLocked(client, target, member)
			}
		}
	} else if target != "" {
		if member := s.nicks[nickKey(target)]; member != nil && !member.hidden() {
			s.whoReplyLocked(client, "*", member)
		}
	} else {
		for _, member := range s.clients {
			if member.registered && !member.hidden() {
				s.whoReplyLocked(client, "*", member)
			}
		}
	}
	s.numericLocked(client, "315", []string{target}, "End of WHO list")
}

func (s *Server) whoReplyLocked(requester *session, channel string, member *session) {
	s.numericLocked(requester, "352", []string{channel, member.client.Username, "localhost", "airc", member.client.Nick, "H"}, "0 "+member.client.RealName)
}

func (s *Server) whoisLocked(client *session, command interface{ Param(int) (string, bool) }) {
	name, _ := command.Param(0)
	if second, ok := command.Param(1); ok {
		name = second
	}
	target := s.nicks[nickKey(name)]
	if target == nil || target.hidden() {
		s.numericLocked(client, "401", []string{name}, "No such nick")
		s.numericLocked(client, "318", []string{name}, "End of WHOIS")
		return
	}
	s.numericLocked(client, "311", []string{target.client.Nick, target.client.Username, "localhost", "*"}, target.client.RealName)
	if len(target.channels) > 0 {
		channels := make([]string, 0, len(target.channels))
		for channel := range target.channels {
			channels = append(channels, channel)
		}
		s.numericLocked(client, "319", []string{target.client.Nick}, strings.Join(channels, " "))
	}
	s.numericLocked(client, "312", []string{target.client.Nick, "airc"}, "Local agent communication")
	s.numericLocked(client, "318", []string{target.client.Nick}, "End of WHOIS")
}

func (s *Server) namesLocked(client *session, command interface{ Param(int) (string, bool) }) {
	list, ok := command.Param(0)
	if !ok || list == "" {
		for channel := range s.channels {
			s.namesOneLocked(client, channel)
		}
		return
	}
	channels := strings.Split(list, ",")
	if len(channels) > 16 {
		s.numericLocked(client, "407", nil, "Too many targets")
		return
	}
	for _, channel := range channels {
		s.namesOneLocked(client, channel)
	}
}

func (s *Server) namesOneLocked(client *session, channel string) {
	members := s.channels[channel]
	if len(members) == 0 {
		s.numericLocked(client, "366", []string{channel}, "End of NAMES list")
		return
	}
	nicks := make([]string, 0, len(members))
	for _, member := range members {
		nicks = append(nicks, member.client.Nick)
	}
	var chunk []string
	chunkSize := 0
	for _, nick := range nicks {
		if chunkSize+len(nick)+1 > 7000 && len(chunk) > 0 {
			s.numericLocked(client, "353", []string{"=", channel}, strings.Join(chunk, " "))
			chunk, chunkSize = nil, 0
		}
		chunk = append(chunk, nick)
		chunkSize += len(nick) + 1
	}
	if len(chunk) > 0 {
		s.numericLocked(client, "353", []string{"=", channel}, strings.Join(chunk, " "))
	}
	s.numericLocked(client, "366", []string{channel}, "End of NAMES list")
}

func (s *Server) listLocked(client *session) {
	for channel, members := range s.channels {
		s.numericLocked(client, "322", []string{channel, strconv.Itoa(len(members))}, "")
	}
	s.numericLocked(client, "323", nil, "End of LIST")
}

// historyLocked serves HISTORY <channel|nick> [limit] [after-message-id].
// A nickname target returns the direct messages addressed to that nickname, which
// is how an agent that was not connected reads what was sent to it. The end
// marker carries a status (ok, more, expired) so callers can page reliably.
func (s *Server) historyLocked(client *session, command protocol.Command) {
	target, ok := command.Param(0)
	if !ok || !(target == protocol.AllDirectMessages || validChannel(target) || validNick(target) || (strings.HasPrefix(target, "@") && validNick(target[1:]))) {
		s.numericLocked(client, "461", []string{"HISTORY"}, "HISTORY requires a channel, nickname, @nickname or @*")
		return
	}
	limit := 50
	if raw, ok := command.Param(1); ok {
		if value, err := strconv.Atoi(raw); err == nil && value > 0 && value <= 1000 {
			limit = value
		}
	}
	after, _ := command.Param(2)
	if after == "-" {
		after = ""
	}
	messages, status := s.history.since(target, after, limit)
	for _, message := range messages {
		s.numericLocked(client, "760", []string{target}, encodeMessage(message))
	}
	params := []string{target, status}
	if s.history.size > 0 {
		cursor := s.history.at(s.history.size - 1).ID
		if status == historyMore || status == historyExpired {
			if len(messages) > 0 {
				cursor = messages[len(messages)-1].ID
			}
		}
		params = append(params, cursor)
	}
	s.numericLocked(client, "761", params, "End of history")
}

func (s *Server) agentsLocked(client *session) {
	entries := make([]agentListing, 0, len(s.clients))
	for _, member := range s.clients {
		if !member.registered || member.hidden() || member == client {
			continue
		}
		channels := make([]string, 0, len(member.channels))
		for channel := range member.channels {
			channels = append(channels, channel)
		}
		sort.Strings(channels)
		entries = append(entries, agentListing{Nick: member.client.Nick, Channels: channels, ConnectedAt: member.client.ConnectedAt})
	}
	sort.Slice(entries, func(i, j int) bool { return strings.ToLower(entries[i].Nick) < strings.ToLower(entries[j].Nick) })
	for _, entry := range entries {
		encoded, _ := json.Marshal(entry)
		s.numericLocked(client, "763", nil, string(encoded))
	}
	s.numericLocked(client, "764", nil, "End of agents list")
}
