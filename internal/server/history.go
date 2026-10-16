package server

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/Someblueman/airc/internal/atomicfile"
	"github.com/Someblueman/airc/internal/protocol"
)

// History query statuses reported in the end-of-history numeric.
const (
	historyOK      = "ok"      // everything requested was returned
	historyMore    = "more"    // the limit was reached; more messages follow the last one returned
	historyExpired = "expired" // the cursor is not in the retained window; the latest messages were returned instead
)

type historyRing struct {
	items     []Message
	start     int
	size      int
	limit     int
	positions map[string]int
	mentions  [][]string
	requests  map[string]int
	quotas    map[string]roomSettings
	// counts is the number of retained messages per target, kept in step with
	// every insert and removal so quota decisions need no scan of the ring.
	counts map[string]int
}

func (h *historyRing) uncount(target string) {
	if h.counts[target] <= 1 {
		delete(h.counts, target)
	} else {
		h.counts[target]--
	}
}

func newHistory(limit int) historyRing {
	if limit < 0 {
		limit = 0
	}
	return historyRing{items: make([]Message, limit), mentions: make([][]string, limit), positions: make(map[string]int, limit), requests: make(map[string]int), counts: make(map[string]int), limit: limit}
}

func (h *historyRing) add(message Message) []string {
	h.makeRoom(message.Target)
	var mentions []string
	if protocol.IsChannel(message.Target) {
		mentions = protocol.Mentions(message.Body)
	}
	if h.limit == 0 {
		return mentions
	}
	index := h.start
	if h.size < h.limit {
		index = (h.start + h.size) % h.limit
		h.size++
	} else {
		old := h.items[index]
		h.uncount(old.Target)
		delete(h.requests, requestKey(old.From, old.AccountID, old.RequestID))
		if previous := h.items[index].ID; h.positions[previous] == index {
			delete(h.positions, previous)
		}
		h.start = (h.start + 1) % h.limit
	}
	h.items[index], h.mentions[index] = message, mentions
	h.counts[message.Target]++
	h.positions[message.ID] = index
	if original, found := h.positions[message.Supersedes]; message.Supersedes != "" && found {
		h.items[original].SupersededBy = message.ID
		h.items[original].Retracted = message.Kind == "retract"
	}
	if key := requestKey(message.From, message.AccountID, message.RequestID); key != "" {
		h.requests[key] = index
	}
	return mentions
}

func (h *historyRing) at(i int) Message { return h.items[(h.start+i)%h.limit] }

// matchesAt compares channels exactly and nicknames case-insensitively.
// "@nick" selects what needs that agent's attention: direct messages to it, and
// channel messages from others that tag or address it.
func (h *historyRing) matchesAt(i int, target string) bool {
	index := (h.start + i) % h.limit
	message := &h.items[index]
	if id, replies, ok := protocol.ConversationTarget(target); ok {
		if replies {
			return message.ReplyTo == id && message.Reaction == ""
		}
		return message.ID == id || message.ThreadID == id
	}
	if target == protocol.AllDirectMessages {
		return !protocol.IsChannel(message.Target)
	}
	if protocol.IsChannel(target) {
		return message.Target == target
	}
	if nick, ok := strings.CutPrefix(target, "@"); ok {
		if strings.EqualFold(message.Target, nick) {
			return true
		}
		if !protocol.IsChannel(message.Target) || strings.EqualFold(message.From, nick) {
			return false
		}
		for _, mention := range h.mentions[index] {
			if strings.EqualFold(mention, nick) {
				return true
			}
		}
		return false
	}
	return strings.EqualFold(message.Target, target)
}

func (h *historyRing) recent(target string, limit int) []Message {
	if limit <= 0 || limit > h.size {
		limit = h.size
	}
	out := make([]Message, 0, limit)
	for i := h.size - 1; i >= 0 && len(out) < limit; i-- {
		if h.matchesAt(i, target) {
			out = append(out, h.at(i))
		}
	}
	slices.Reverse(out)
	return out
}

// since returns history for target. Without a cursor it returns the latest limit
// messages. With a cursor it returns the oldest limit messages after the cursor,
// so a caller that pages forward never skips anything. A cursor that has left
// the retained window cannot be resumed reliably, so the latest messages are
// returned with historyExpired and the caller decides what to do.
func (h *historyRing) since(target, after string, limit int) ([]Message, string) {
	if after == "" {
		return h.recent(target, limit), historyOK
	}
	cursor := -1
	if after != "*" {
		if index, found := h.positions[after]; found {
			cursor = (index - h.start + h.limit) % h.limit
		}
	}
	if cursor < 0 && after != "*" {
		return h.recent(target, limit), historyExpired
	}
	if limit <= 0 {
		limit = h.size
	}
	out := make([]Message, 0, min(limit, h.size-cursor-1))
	for i := cursor + 1; i < h.size; i++ {
		if !h.matchesAt(i, target) {
			continue
		}
		if len(out) == limit {
			return out, historyMore
		}
		out = append(out, h.at(i))
	}
	return out, historyOK
}

// recordLocked holds mu and messageMu on entry and return, but releases mu for
// file I/O and both while it waits for its sync. Posts take messageMu back in
// message order (see groupCommit), so appends, broadcasts and receipts keep
// their order while queries, connection cleanup and later appends proceed.
func (s *Server) recordLocked(message *Message) []string {
	mentions := s.history.add(*message)
	if s.histFile == nil && s.commit.idle() {
		return mentions
	}
	if s.messageMu.TryLock() {
		// A posting command missing from serializesPosts would interleave
		// appends with compaction; fail that one command loudly instead.
		s.messageMu.Unlock()
		panic("recordLocked called without messageMu")
	}
	// Without a sync to wait for, handing messageMu over would cost more
	// than it saves, unless earlier posts are still waiting for theirs.
	grouped := s.cfg.Sync != atomicfile.SyncNone || !s.commit.idle()
	var ticket uint64
	if grouped {
		ticket = s.commit.issue()
	}
	file := s.histFile
	var err error
	if file != nil {
		s.mu.Unlock()
		var line []byte
		line, err = json.Marshal(message)
		if err == nil {
			_, err = file.Write(append(line, '\n'))
		}
		s.mu.Lock()
		if err == nil {
			s.histBytes += int64(len(line) + 1)
			s.histRecords++
			if grouped {
				s.commit.wrote(ticket)
			}
		} else {
			// Append nothing more; the file is closed once the posts
			// written before this one have their sync.
			s.histFile = nil
		}
	}
	if grouped {
		s.mu.Unlock()
		s.messageMu.Unlock()
		if file != nil && err == nil {
			err = s.commit.waitDurable(ticket)
		}
		s.commit.awaitTurn(ticket)
		s.messageMu.Lock()
		s.commit.resume(ticket)
		s.mu.Lock()
	}
	if file == nil {
		return mentions
	}
	if err != nil {
		s.persistenceError = err.Error()
		if s.histFile == file {
			s.histFile = nil
		}
		// Keep serving from memory; retrying a broken file would only spam
		// the log. Every post of a failed sync reports it; one logs it.
		if s.commit.retire(file) {
			s.logger.Error("history_write_failed", "error", err.Error())
		}
		return mentions
	}
	message.Persisted = true
	if index, ok := s.history.positions[message.ID]; ok {
		s.history.items[index].Persisted = true
	}
	if s.histFile == file && s.histRecords >= s.histCompactAfter && (s.histRecords >= max(2, 2*s.history.limit) || s.histBytes > int64(max(1<<20, s.history.limit*32768))) {
		// A failed compaction leaves a valid descriptor: keep appending and
		// try again later, so a passing fault does not switch durability off.
		if compactErr := s.compactHistoryLocked(); compactErr != nil {
			s.persistenceError = compactErr.Error()
			s.histCompactAfter = s.histRecords + max(16, s.history.limit/4)
			s.logger.Error("history_compaction_failed", "error", compactErr.Error(), "retry_after_records", s.histCompactAfter)
		} else {
			s.persistenceError, s.histCompactAfter = "", 0
		}
	}
	return mentions
}
