package server

import (
	"cmp"
	"encoding/json"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/internal/version"
)

// agentListing is one agent in AGENTS. An agent with a persistent session
// has its joined channels and when it connected; with ACTIVE, a one-shot agent
// active within the last ten minutes, or whose status still stands, is listed
// too, with the rooms it used and when it was last active, and no connection
// time. Status is the agent's latest status post while the daemon holds it.
type agentListing struct {
	Nick        string                `json:"nick"`
	Channels    []string              `json:"channels"`
	ConnectedAt time.Time             `json:"connected_at,omitzero"`
	ActiveAt    time.Time             `json:"active_at,omitzero"`
	Status      *protocol.AgentStatus `json:"status,omitempty"`
}

func (s *Server) statusLocked(client *session) {
	observers := 0
	for _, other := range s.clients {
		if len(other.watching) > 0 {
			observers++
		}
	}
	var carriers []string
	for _, c := range s.cfg.Carriers {
		carriers = append(carriers, c.String())
	}
	var archived int64
	if s.archive != nil {
		_, archived = s.archive.stats()
		archived += s.histBytes
	}
	data, _ := json.Marshal(protocol.ServerStatus{
		Carriers:  carriers,
		Observers: observers,
		TLS:       s.cfg.TLSConfig != nil, AccessRequired: s.accessEnabled, Version: version.String(), PID: os.Getpid(), Connections: len(s.clients),
		MaxConnections: s.cfg.MaxConnections, HistoryLimit: s.cfg.HistoryLimit,
		HistorySize: s.history.size, HistoryFile: s.histFile != nil, PersistenceError: cmp.Or(s.persistenceError, s.env.failed),
		HistoryBytes: s.history.bytes, HistoryMaxBytes: s.history.maxBytes, ArchiveBytes: archived,
	})
	s.numericLocked(client, "770", nil, string(data))
}

func (s *Server) whoLocked(client *session, command interface{ Param(int) (string, bool) }) {
	target, _ := command.Param(0)
	if protocol.IsChannel(target) {
		for _, member := range s.channels[target] {
			if !member.hidden() {
				s.whoReplyLocked(client, target, member)
			}
		}
	} else if target != "" {
		if member := s.liveNickLocked(target); member != nil && !member.hidden() {
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
	flags := "H"
	if member.away != "" {
		flags = "G"
	}
	if s.channelOperator(member, channel) {
		flags += "@"
	}
	s.numericLocked(requester, "352", []string{channel, member.client.Username, "localhost", "airc", member.client.Nick, flags}, "0 "+member.client.RealName)
}

func (s *Server) whoisLocked(client *session, command interface{ Param(int) (string, bool) }) {
	name, _ := command.Param(0)
	if second, ok := command.Param(1); ok {
		name = second
	}
	target := s.liveNickLocked(name)
	if target == nil || target.hidden() {
		s.numericLocked(client, "401", []string{name}, "No such nick")
		s.numericLocked(client, "318", []string{name}, "End of WHOIS")
		return
	}
	if target.away != "" {
		s.numericLocked(client, "301", []string{target.client.Nick}, target.away)
	}
	s.numericLocked(client, "311", []string{target.client.Nick, target.client.Username, "localhost", "*"}, target.client.RealName)
	if len(target.channels) > 0 {
		channels := make([]string, 0, len(target.channels))
		for channel := range target.channels {
			if s.channelOperator(target, channel) {
				channel = "@" + channel
			}
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
	channels, ok := s.targetListLocked(client, list)
	if !ok {
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
		nick := member.client.Nick
		if s.channelOperator(member, channel) {
			nick = "@" + nick
		}
		nicks = append(nicks, nick)
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
	selected, err := s.historyTarget(target)
	if !ok || err != nil {
		if strings.HasPrefix(target, "thread:") || strings.HasPrefix(target, "replies:") {
			s.numericLocked(client, "430", []string{target}, err.Error())
		} else {
			s.numericLocked(client, "461", []string{"HISTORY"}, "HISTORY requires a channel, nickname, @nickname or @*")
		}
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
	// The fourth parameter is the options of RUN daemons: "*", or run=ID.
	rawOptions, _ := command.Param(3)
	options, err := parseHistoryOptions(rawOptions)
	if err != nil {
		s.numericLocked(client, "461", []string{"HISTORY"}, "HISTORY options: "+err.Error())
		return
	}
	var accept func(*entry) bool
	if options.run != "" {
		accept = options.matchesRun
	}
	var messages []Message
	status, next := historyOK, ""
	if after == "" {
		messages = s.history.recentWhere(selected, limit, accept)
	} else {
		var found bool
		messages, status, next, found = s.readAfterLocked(selected, after, limit, accept)
		if !found {
			messages, status = s.history.recentWhere(selected, limit, accept), historyExpired
		}
	}
	for _, message := range messages {
		s.numericLocked(client, "760", []string{target}, encodeMessage(message))
	}
	params := []string{target, status}
	if newest, ok := s.history.last(); ok {
		cursor := newest.ID
		switch {
		case status == historyMore:
			cursor = next
		case status == historyExpired && len(messages) > 0:
			cursor = messages[len(messages)-1].ID
		}
		params = append(params, cursor)
	}
	s.numericLocked(client, "761", params, "End of history")
}

func (s *Server) agentsLocked(client *session, command protocol.Command) {
	entries := make([]agentListing, 0, len(s.clients))
	listed := map[string]bool{}
	now := s.now()
	for _, member := range s.clients {
		if !member.registered || member.hidden() || member == client {
			continue
		}
		channels := make([]string, 0, len(member.channels))
		for channel := range member.channels {
			channels = append(channels, channel)
		}
		sort.Strings(channels)
		entries = append(entries, agentListing{Nick: member.client.Nick, Channels: channels, ConnectedAt: member.client.ConnectedAt, Status: s.agentStatusLocked(member.client.Nick, now)})
		listed[nickKey(member.client.Nick)] = true
	}
	// Older clients never ask, so they never see an entry without a
	// connection time.
	if mode, _ := command.Param(0); strings.EqualFold(mode, "ACTIVE") {
		for key, a := range s.activity {
			if listed[key] || key == nickKey(client.client.Nick) {
				continue
			}
			status := s.agentStatusLocked(a.nick, now)
			if seen, rooms, ok := s.recentLocked(a.nick, now); ok || status != nil {
				entries = append(entries, agentListing{Nick: a.nick, Channels: append([]string{}, rooms...), ActiveAt: cmp.Or(seen, a.seen), Status: status})
			}
		}
	}
	sort.Slice(entries, func(i, j int) bool { return strings.ToLower(entries[i].Nick) < strings.ToLower(entries[j].Nick) })
	for _, entry := range entries {
		encoded, _ := json.Marshal(entry)
		s.numericLocked(client, "763", nil, string(encoded))
	}
	s.numericLocked(client, "764", nil, "End of agents list")
}
