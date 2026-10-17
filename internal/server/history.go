package server

import (
	"encoding/json"

	"github.com/Someblueman/airc/internal/atomicfile"
)

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
	if e := s.history.entryByID(message.ID); e != nil {
		e.Persisted = true
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
