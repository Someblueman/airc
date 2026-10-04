package server

import (
	"maps"
	"slices"
	"strings"

	"github.com/Someblueman/airc/internal/protocol"
)

func (s *Server) awayLocked(c *session, cmd protocol.Command) {
	if c.hidden() {
		s.numericLocked(c, "484", nil, "AWAY requires a persistent session; use PRESENCE away with a TTL for one-shot agents")
		return
	}
	if !protocol.BriefText(cmd.Trailing, 240) {
		s.numericLocked(c, "417", nil, "Away reason exceeds 240 bytes")
		return
	}
	c.away = strings.TrimSpace(cmd.Trailing)
	code, text := "305", "You are no longer marked away"
	if c.away != "" {
		code, text = "306", "You have been marked away"
	}
	s.numericLocked(c, code, nil, text)
}

func (s *Server) monitorStatusLocked(c *session, nick string) {
	live := s.liveNickLocked(nick)
	if live == nil || !live.registered || live.hidden() {
		s.numericLocked(c, "731", nil, nick)
		return
	}
	s.numericLocked(c, "730", nil, live.client.Nick+"!"+live.client.Username+"@localhost")
}

func (s *Server) monitorChangedLocked(subject *session, online bool) {
	for _, c := range s.clients {
		if _, ok := c.monitoring[nickKey(subject.client.Nick)]; !ok {
			continue
		}
		if online {
			s.numericLocked(c, "730", nil, subject.client.Nick+"!"+subject.client.Username+"@localhost")
		} else {
			s.numericLocked(c, "731", nil, subject.client.Nick)
		}
	}
}

func (s *Server) monitorLocked(c *session, cmd protocol.Command) {
	action, _ := cmd.Param(0)
	list, _ := cmd.Param(1)
	if list == "" {
		list = cmd.Trailing
	}
	if c.monitoring == nil {
		c.monitoring = map[string]string{}
	}
	switch strings.ToUpper(action) {
	case "+", "-":
		nicks := strings.Split(list, ",")
		if len(nicks) > 128 {
			s.numericLocked(c, "734", []string{"128", list}, "Monitor list is full")
			return
		}
		for _, nick := range nicks {
			if !validNick(nick) {
				s.numericLocked(c, "432", []string{nick}, "Invalid monitor nickname")
				continue
			}
			key := nickKey(nick)
			if action == "-" {
				delete(c.monitoring, key)
				continue
			}
			if _, exists := c.monitoring[key]; !exists && len(c.monitoring) >= 128 {
				s.numericLocked(c, "734", []string{"128", nick}, "Monitor list is full")
				continue
			}
			c.monitoring[key] = nick
			s.monitorStatusLocked(c, nick)
		}
	case "C":
		c.monitoring = nil
	case "L", "S":
		keys := slices.Sorted(maps.Keys(c.monitoring))
		for _, key := range keys {
			if strings.EqualFold(action, "S") {
				s.monitorStatusLocked(c, c.monitoring[key])
			} else {
				s.numericLocked(c, "732", nil, c.monitoring[key])
			}
		}
		if strings.EqualFold(action, "L") {
			s.numericLocked(c, "733", nil, "End of MONITOR list")
		}
	default:
		s.numericLocked(c, "461", []string{"MONITOR"}, "Use MONITOR +|- nick,nick or C|L|S")
	}
}

// A nickname claim during CAP/NICK/USER negotiation is not a live recipient.
func (s *Server) liveNickLocked(nick string) *session {
	c := s.nicks[nickKey(nick)]
	if c == nil || !c.registered || c.hidden() {
		return nil
	}
	return c
}
