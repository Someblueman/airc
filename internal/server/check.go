package server

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/Someblueman/airc/internal/protocol"
)

// CHECK streams at most 1000 messages, 64 page markers, 64 topics, 64 charters and 128 pins.
// It holds mu for a consistent snapshot, without disk I/O or retained caches.
func (s *Server) checkLocked(client *session, command protocol.Command) {
	var r protocol.CheckRequest
	if json.Unmarshal([]byte(command.Trailing), &r) != nil || len(r.Targets) == 0 || len(r.Targets) > 64 || r.MaxMessages < 1 || r.MaxMessages > 1000 {
		s.numericLocked(client, "461", nil, "CHECK requires 1-64 targets and max_messages 1-1000")
		return
	}
	var rooms []string
	seen := map[string]bool{}
	selected := make([]string, len(r.Targets))
	warnings := make([]string, len(r.Targets))
	for i, t := range r.Targets {
		if seen[t.Target] {
			s.numericLocked(client, "461", nil, "CHECK targets must be unique")
			return
		}
		seen[t.Target] = true
		if r.Headers && validChannel(t.Target) {
			rooms = append(rooms, t.Target)
		}
		if t.Limit < 1 || t.Limit > 1000 || t.After != "" && t.After != "*" && !protocol.ValidMessageID(t.After) {
			s.numericLocked(client, "461", nil, "invalid CHECK limit or cursor")
			return
		}
		var err error
		selected[i], err = s.historyTarget(t.Target)
		if err != nil {
			if strings.HasPrefix(t.Target, "thread:") {
				warnings[i] = "Followed " + t.Target + " expired; use airc unfollow to remove it"
			} else {
				s.numericLocked(client, "430", nil, err.Error())
				return
			}
		}
	}
	for _, room := range rooms {
		s.checkEntryLocked(client, protocol.CheckEntry{Kind: "topic", Target: room, Topic: s.topics[room].Text})
		s.checkEntryLocked(client, protocol.CheckEntry{Kind: "charter", Target: room, Charter: s.chat.Rooms[room].Charter})
		for _, pin := range s.chat.Pins[room] {
			m := messageMetadata(s.annotated(pin))
			s.checkEntryLocked(client, protocol.CheckEntry{Kind: "pin", Target: room, Message: &m})
		}
	}
	type page struct {
		messages []Message
		end      protocol.CheckEntry
	}
	if !client.observer {
		checked := make([]string, 0, len(r.Targets))
		for _, t := range r.Targets {
			checked = append(checked, t.Target)
		}
		s.noteActivityLocked(client.client.Nick, checked...)
	}
	pages := make([]page, len(r.Targets))
	unique := map[string]Message{}
	for i, t := range r.Targets {
		p := &pages[i]
		p.end = protocol.CheckEntry{Kind: "page", Target: t.Target, Status: historyOK, Warning: warnings[i]}
		if warnings[i] != "" {
			continue
		}
		var messages []Message
		status, next := historyOK, ""
		if t.After == "" {
			messages = s.history.recent(selected[i], t.Limit)
		} else {
			accept := func(e *entry) bool { return r.IncludeOwn || !strings.EqualFold(e.From, client.client.Nick) }
			var found bool
			messages, status, next, found = s.readAfterLocked(selected[i], t.After, t.Limit, accept)
			if !found {
				p.end.Gap = true
				messages, status, next = s.windowPageLocked(selected[i], 0, t.Limit, accept, nil)
			}
		}
		for _, m := range messages {
			if !r.IncludeOwn && strings.EqualFold(m.From, client.client.Nick) {
				continue
			}
			p.messages = append(p.messages, m)
			unique[m.ID] = m
		}
		p.end.Status = status
		if newest, ok := s.history.last(); ok {
			p.end.Cursor = newest.ID
		}
		if status == historyMore {
			p.end.Cursor = next
		}
	}
	ordered := make([]Message, 0, len(unique))
	for _, m := range unique {
		ordered = append(ordered, m)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Seq < ordered[j].Seq })
	if len(ordered) > r.MaxMessages {
		ordered = ordered[:r.MaxMessages]
	}
	included := map[string]bool{}
	for _, m := range ordered {
		included[m.ID] = true
	}
	// Stop each page at its first excluded message, so every cursor describes
	// a contiguous prefix, even when rooms, mentions and threads overlap.
	associations := map[string][]int{}
	for i := range pages {
		p := &pages[i]
		last := r.Targets[i].After
		for _, m := range p.messages {
			if !included[m.ID] {
				p.end.Status = historyMore
				p.end.Cursor = last
				break
			}
			associations[m.ID] = append(associations[m.ID], i)
			last = m.ID
		}
	}
	for _, m := range ordered {
		metadata := messageMetadata(m)
		s.checkEntryLocked(client, protocol.CheckEntry{Kind: "message", Target: m.Target, Targets: associations[m.ID], Message: &metadata})
	}
	for _, p := range pages {
		s.checkEntryLocked(client, p.end)
	}
	s.numericLocked(client, "785", nil, "End of check")
}

func (s *Server) checkEntryLocked(client *session, e protocol.CheckEntry) {
	data, _ := json.Marshal(e)
	s.numericLocked(client, "784", nil, string(data))
}
