package server

import (
	"errors"
	"runtime"
	"strconv"
	"strings"

	"github.com/Someblueman/airc/internal/protocol"
)

func (s *Server) historyTarget(target string) (string, error) {
	if strings.HasPrefix(target, "thread:") || strings.HasPrefix(target, "replies:") {
		return s.history.conversation(target)
	}
	if target == protocol.AllDirectMessages || validChannel(target) || protocol.ValidNick(target) || strings.HasPrefix(target, "@") && protocol.ValidNick(target[1:]) {
		return target, nil
	}
	return "", errors.New("history target must be a channel, nickname, inbox or conversation")
}

// search pages through the retained window like since, matching a substring
// of message bodies and optionally a sender. It reads at most searchChunk
// messages between calls to yield, which releases the server lock, so a
// search of the whole window never stalls the server; positions are stable,
// so the scan continues where it stopped.
func (h *historyRing) search(target, after, query, from string, limit int, yield func()) ([]Message, string) {
	start, found := h.start(after)
	if !found {
		return nil, historyExpired
	}
	matcher := newSearchText(query)
	messages := make([]Message, 0, min(limit, 64))
	for {
		read, done, more := 0, true, false
		h.forward(h.selectTarget(target), start, func(p uint64, e *entry) bool {
			if read == searchChunk {
				start, done = p, false
				return false
			}
			read++
			if from != "*" && !strings.EqualFold(e.From, from) || !matcher.contains(e.Body) {
				return true
			}
			if len(messages) == limit {
				more = true
				return false
			}
			messages = append(messages, e.Message)
			return true
		})
		if more {
			return messages, historyMore
		}
		if done {
			return messages, historyOK
		}
		yield()
	}
}

const searchChunk = 4096

func (s *Server) searchLocked(client *session, command protocol.Command) {
	target, _ := command.Param(0)
	rawLimit, _ := command.Param(1)
	after, _ := command.Param(2)
	from, _ := command.Param(3)
	limit, err := strconv.Atoi(rawLimit)
	if err != nil || limit < 1 || limit > 1000 || after != "*" && !protocol.ValidMessageID(after) || from != "*" && !protocol.ValidNick(from) || strings.TrimSpace(command.Trailing) == "" || !protocol.BriefText(command.Trailing, 256) {
		s.numericLocked(client, "461", nil, "SEARCH requires target, limit 1-1000, cursor or *, sender or *, and a query of at most 256 bytes")
		return
	}
	selected := target
	if target != "*" {
		selected, err = s.historyTarget(target)
		if err != nil {
			s.numericLocked(client, "430", nil, err.Error())
			return
		}
	}
	messages, status := s.history.search(selected, after, command.Trailing, from, limit, func() {
		s.mu.Unlock()
		runtime.Gosched() // let waiting goroutines take the lock
		s.mu.Lock()
	})
	for _, message := range messages {
		s.numericLocked(client, "760", []string{target}, encodeMessage(message))
	}
	params := []string{target, status}
	if len(messages) > 0 {
		params = append(params, messages[len(messages)-1].ID)
	}
	s.numericLocked(client, "761", params, "End of search")
}
