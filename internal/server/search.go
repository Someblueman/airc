package server

import (
	"errors"
	"strconv"
	"strings"

	"github.com/Someblueman/airc/internal/protocol"
)

func (s *Server) historyTarget(target string) (string, error) {
	if strings.HasPrefix(target, "thread:") || strings.HasPrefix(target, "replies:") {
		return s.history.conversation(target)
	}
	if target == protocol.AllDirectMessages || validChannel(target) || validNick(target) || strings.HasPrefix(target, "@") && validNick(target[1:]) {
		return target, nil
	}
	return "", errors.New("history target must be a channel, nickname, inbox or conversation")
}

// Search scans the same finite ring as history. No second index retains pruned messages.
func (h *historyRing) search(target, after, query, from string, limit int) ([]Message, string) {
	cursor := -1
	if after != "*" {
		index, found := h.positions[after]
		if !found {
			return nil, historyExpired
		}
		cursor = (index - h.start + h.limit) % h.limit
	}
	matcher := newSearchText(query)
	messages := make([]Message, 0, min(limit, h.size))
	for i := cursor + 1; i < h.size; i++ {
		message := h.at(i)
		if target != "*" && !h.matchesAt(i, target) || from != "*" && !strings.EqualFold(message.From, from) || !matcher.contains(message.Body) {
			continue
		}
		if len(messages) == limit {
			return messages, historyMore
		}
		messages = append(messages, message)
	}
	return messages, historyOK
}

func (s *Server) searchLocked(client *session, command protocol.Command) {
	target, _ := command.Param(0)
	rawLimit, _ := command.Param(1)
	after, _ := command.Param(2)
	from, _ := command.Param(3)
	limit, err := strconv.Atoi(rawLimit)
	if err != nil || limit < 1 || limit > 1000 || after != "*" && !protocol.ValidMessageID(after) || from != "*" && !validNick(from) || strings.TrimSpace(command.Trailing) == "" || !protocol.BriefText(command.Trailing, 256) {
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
	messages, status := s.history.search(selected, after, command.Trailing, from, limit)
	for _, message := range messages {
		s.numericLocked(client, "760", []string{target}, encodeMessage(message))
	}
	params := []string{target, status}
	if len(messages) > 0 {
		params = append(params, messages[len(messages)-1].ID)
	}
	s.numericLocked(client, "761", params, "End of search")
}
