package server

import (
	"math"
	"runtime"

	"github.com/Someblueman/airc/internal/protocol"
)

// readChunk bounds the window messages one read examines between releases of
// the server lock, so a sparse search of a large window never stalls it.
const readChunk = 4096

// readAfterLocked returns up to limit of target's messages after the cursor
// after (a message ID, or "*" for the start of the window), oldest first,
// with those accept rejects left out. Status is historyMore when more may
// follow, with cursor the place to continue from. A cursor older than the
// window resumes from the archive, which is read with mu released and
// continues into the window. found is false when the cursor is in neither.
func (s *Server) readAfterLocked(target, after string, limit int, accept func(*entry) bool) (out []Message, status, cursor string, found bool) {
	if from, ok := s.history.start(after); ok {
		out, status, cursor = s.windowPageLocked(target, from, limit, accept, nil)
		return out, status, cursor, true
	}
	if s.archive == nil {
		return nil, historyExpired, "", false
	}
	// The active file's messages are mapped in memory; the sealed segments
	// are searched without the lock.
	loc, last, ok := location{}, uint64(0), false
	if record, found := s.histActive[after]; found {
		loc, last, ok = location{s.histFirstSeq, record.next}, record.seq, true
	} else {
		s.mu.Unlock()
		loc, last, ok = s.archive.find(after, segment{})
		s.mu.Lock()
	}
	if !ok {
		return nil, historyExpired, "", false
	}
	keys, needle := archiveKeys(target), targetNeedle(target)
	more := false
	for {
		stop, retained := s.history.firstSeq()
		if !retained {
			stop = math.MaxUint64
		}
		active := s.activeSegmentLocked()
		s.mu.Unlock()
		budget := min(archiveBudget, max(s.histSegment/2, 1))
		result, err := s.archive.scan(loc, active, stop, keys, needle, budget, func(m Message) bool {
			e := archivedEntry(m)
			if !matchesTarget(e, target) || accept != nil && !accept(e) {
				return true
			}
			if len(out) == limit {
				more = true
				return false
			}
			out = append(out, m)
			return true
		})
		s.mu.Lock()
		last = max(last, result.lastSeq)
		if more {
			return out, historyMore, out[len(out)-1].ID, true
		}
		if result.exhausted {
			return out, historyMore, result.lastID, true
		}
		// Stop when the window has not moved past what was read. An error
		// means retention removed the rest of the archived range meanwhile;
		// what follows in the window is still in order.
		loc = result.next
		if next, ok := s.history.firstSeq(); err != nil || !ok || next <= stop {
			break
		}
	}
	rest, status, cursor := s.windowPageLocked(target, s.history.afterSeq(last), limit-len(out), accept, out)
	return rest, status, cursor, true
}

// archivedEntry wraps an archived message for the window's matching.
func archivedEntry(m Message) *entry {
	e := &entry{Message: m}
	if protocol.IsChannel(m.Target) {
		e.mentions = protocol.MessageMentions(m.Body, m.For, m.GroupMentions)
	}
	return e
}

// windowPageLocked appends to out up to limit selected window messages from
// position from. It releases mu every readChunk messages examined.
func (s *Server) windowPageLocked(target string, from uint64, limit int, accept func(*entry) bool, out []Message) ([]Message, string, string) {
	added := 0
	for {
		examined, done, more := 0, true, false
		s.history.forward(s.history.selectTarget(target), from, func(p uint64, e *entry) bool {
			if examined == readChunk {
				from, done = p, false
				return false
			}
			examined++
			if accept != nil && !accept(e) {
				return true
			}
			if added == limit {
				more = true
				return false
			}
			out = append(out, e.Message)
			added++
			return true
		})
		if more {
			return out, historyMore, out[len(out)-1].ID
		}
		if done {
			return out, historyOK, ""
		}
		s.mu.Unlock()
		runtime.Gosched() // let waiting goroutines take the lock
		s.mu.Lock()
	}
}
