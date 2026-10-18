package server

import (
	"errors"
	"math"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// historyArchived ends a newest-first search that reached the start of the
// memory window while older messages remain in the archive on disk. An
// oldest-first search, which reads the archive, reaches them.
const historyArchived = "archived"

// maxSearchTerms bounds the terms of one query, so a request cannot make each
// message cost many scans.
const maxSearchTerms = 16

// searchOptions is the fifth SEARCH parameter of SEARCH_TERMS daemons: "*",
// or a comma-separated list of terms, newest, since=UNIX and until=UNIX.
type searchOptions struct {
	terms        bool
	newest       bool
	since, until time.Time // zero when unbounded; since inclusive, until exclusive
}

func parseSearchOptions(raw string) (searchOptions, error) {
	var o searchOptions
	if raw == "" || raw == "*" {
		return o, nil
	}
	seen := map[string]bool{}
	for option := range strings.SplitSeq(raw, ",") {
		name, value, hasValue := strings.Cut(option, "=")
		if seen[name] {
			return o, errors.New("repeated search option " + name)
		}
		seen[name] = true
		switch {
		case name == "terms" && !hasValue:
			o.terms = true
		case name == "newest" && !hasValue:
			o.newest = true
		case (name == "since" || name == "until") && hasValue:
			seconds, err := strconv.ParseInt(value, 10, 64)
			if err != nil || seconds < 0 || seconds > 1<<40 {
				return o, errors.New(name + " must be Unix seconds")
			}
			if name == "since" {
				o.since = time.Unix(seconds, 0)
			} else {
				o.until = time.Unix(seconds, 0)
			}
		default:
			return o, errors.New("unknown search option " + option)
		}
	}
	if !o.since.IsZero() && !o.until.IsZero() && !o.since.Before(o.until) {
		return o, errors.New("since must be before until")
	}
	return o, nil
}

// inTime reports whether t falls in the options' time window.
func (o searchOptions) inTime(t time.Time) bool {
	return (o.since.IsZero() || !t.Before(o.since)) && (o.until.IsZero() || t.Before(o.until))
}

// searchMatcher matches a message body against a parsed query: every include
// must occur and no exclude may, each a case-insensitive substring.
type searchMatcher struct {
	include, exclude []searchText
}

// parseSearchTerms splits a query into terms. Whitespace separates terms, a
// double-quoted phrase is one term (an unclosed quote runs to the end), and a
// leading - excludes a term or phrase. At least one term must be included.
func parseSearchTerms(query string) (searchMatcher, error) {
	var m searchMatcher
	rest := strings.TrimSpace(query)
	for rest != "" {
		exclude := false
		if len(rest) > 1 && rest[0] == '-' && rest[1] != ' ' && rest[1] != '\t' {
			exclude, rest = true, rest[1:]
		}
		var term string
		if rest[0] == '"' {
			body, after, closed := strings.Cut(rest[1:], `"`)
			if !closed {
				after = ""
			}
			term, rest = body, after
		} else {
			end := strings.IndexAny(rest, " \t\n")
			if end < 0 {
				end = len(rest)
			}
			term, rest = rest[:end], rest[end:]
		}
		rest = strings.TrimSpace(rest)
		if strings.TrimSpace(term) == "" {
			continue
		}
		if exclude {
			m.exclude = append(m.exclude, newSearchText(term))
		} else {
			m.include = append(m.include, newSearchText(term))
		}
		if len(m.include)+len(m.exclude) > maxSearchTerms {
			return m, errors.New("a search takes at most 16 terms")
		}
	}
	if len(m.include) == 0 {
		return m, errors.New("a search needs at least one term that is not excluded")
	}
	return m, nil
}

func (m searchMatcher) matches(body string) bool {
	for _, term := range m.include {
		if !term.contains(body) {
			return false
		}
	}
	for _, term := range m.exclude {
		if term.contains(body) {
			return false
		}
	}
	return true
}

// readBeforeLocked returns up to limit of target's window messages before the
// cursor before (a message ID, or "*" for the newest), newest first, with
// those accept rejects left out. It stops at the first message older than
// stopBefore when that is set, since a window's timestamps follow its order.
// Status is historyMore with a cursor when the limit was reached, and
// historyArchived when the walk reached the start of the window and older
// messages remain on disk. It releases mu every readChunk messages examined.
func (s *Server) readBeforeLocked(target, before string, limit int, stopBefore time.Time, accept func(*entry) bool) (out []Message, status, cursor string, found bool) {
	bound := uint64(math.MaxUint64)
	if before != "*" {
		p, ok := s.history.positions[before]
		if !ok {
			return nil, historyExpired, "", false
		}
		bound = p
	}
	for {
		examined, done, more, stopped := 0, true, false, false
		s.history.backwardBefore(s.history.selectTarget(target), bound, func(p uint64, e *entry) bool {
			if examined == readChunk {
				bound, done = p+1, false
				return false
			}
			examined++
			if !stopBefore.IsZero() && e.Timestamp.Before(stopBefore) {
				stopped = true
				return false
			}
			if accept != nil && !accept(e) {
				return true
			}
			if len(out) == limit {
				more = true
				return false
			}
			out = append(out, e.Message)
			return true
		})
		switch {
		case more:
			return out, historyMore, out[len(out)-1].ID, true
		case stopped:
			return out, historyOK, "", true
		case done:
			if s.archiveOlderThanWindowLocked() {
				return out, historyArchived, "", true
			}
			return out, historyOK, "", true
		}
		s.mu.Unlock()
		runtime.Gosched() // let waiting goroutines take the lock
		s.mu.Lock()
	}
}

// archiveOlderThanWindowLocked reports whether the history file or its sealed
// segments hold messages older than the memory window.
func (s *Server) archiveOlderThanWindowLocked() bool {
	if s.archive == nil {
		return false
	}
	oldest, ok := s.archive.oldestSeq()
	if !ok {
		if s.histBytes == 0 {
			return false
		}
		oldest = s.histFirstSeq
	}
	first, retained := s.history.firstSeq()
	return !retained || oldest < first
}
