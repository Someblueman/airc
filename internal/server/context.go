package server

import (
	"errors"
	"maps"
	"slices"
	"strings"

	"github.com/Someblueman/airc/internal/protocol"
)

// contextLocked returns original retained text, with correction links intact.
// It never manufactures a summary or moves a reader's cursor.
func (s *Server) contextLocked(client *session, r protocol.ChatRequest) error {
	if !protocol.ValidMessageID(r.ID) || r.Limit < 1 || r.Limit > 1000 {
		return errors.New("context requires a message ID and limit 1-1000")
	}
	if r.MaxBytes != 0 && (r.MaxBytes < 1024 || r.MaxBytes > 1<<20) {
		return errors.New("context max_bytes must be 1024-1048576 or zero")
	}
	target, err := s.history.conversation("thread:" + r.ID)
	if err != nil {
		return err
	}
	root := strings.TrimPrefix(target, "thread:")
	summary := &protocol.ContextSummary{TriggerID: r.ID, RootID: root}
	for _, id := range []string{r.ID, root} {
		if _, ok := s.history.message(id); !ok && (len(summary.Missing) == 0 || summary.Missing[0] != id) {
			summary.Missing = append(summary.Missing, id)
		}
	}
	var all []Message
	participants := map[string]string{}
	room := ""
	for i := 0; i < s.history.size; i++ {
		m := s.history.at(i)
		if m.ID != root && m.ThreadID != root {
			continue
		}
		all = append(all, m)
		participants[nickKey(m.From)] = m.From
		if protocol.IsChannel(m.Target) {
			room = m.Target
		} else {
			participants[nickKey(m.Target)] = m.Target
		}
	}
	// Prioritize the triggering message and root, then the newest retained replies.
	selected := map[string]bool{}
	for _, id := range []string{r.ID, root} {
		if _, ok := s.history.message(id); ok && len(selected) < r.Limit {
			selected[id] = true
		}
	}
	// Keep the latest correction for the triggering message/root before filling
	// the remaining budget with conversation replies.
	for _, id := range []string{r.ID, root} {
		current, ok := s.history.message(id)
		visited := map[string]bool{}
		for ok && current.SupersededBy != "" && !visited[current.ID] {
			visited[current.ID] = true
			next, found := s.history.message(current.SupersededBy)
			if !found {
				break
			}
			current = next
		}
		if ok && len(selected) < r.Limit {
			selected[current.ID] = true
		}
	}
	protected := make(map[string]bool, len(selected))
	for id := range selected {
		protected[id] = true
	}
	for i := len(all) - 1; i >= 0 && len(selected) < r.Limit; i-- {
		selected[all[i].ID] = true
	}
	var messages []Message
	for _, m := range all {
		if selected[m.ID] {
			messages = append(messages, m)
			if r.MaxBytes != 0 && protected[m.ID] {
				summary.ProtectedIDs = append(summary.ProtectedIDs, m.ID)
			}
		}
	}
	keys := slices.Sorted(maps.Keys(participants))
	names := make([]string, 0, min(64, len(keys)))
	for _, key := range keys[:min(64, len(keys))] {
		names = append(names, participants[key])
	}
	// Begin with everything omitted; encoding admits whole records and updates
	// these counts before any response is enqueued.
	summary.OmittedMessages = len(all)
	summary.OmittedPins = len(s.chat.Pins[room])
	summary.OmittedProfiles = len(keys)
	return s.sendContextLocked(client, messages, s.chat.Pins[room], names, protected, summary, r.MaxBytes)
}
