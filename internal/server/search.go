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
	if target == protocol.AllDirectMessages || validChannel(target) || protocol.ValidNick(target) || strings.HasPrefix(target, "@") && protocol.ValidNick(target[1:]) {
		return target, nil
	}
	return "", errors.New("history target must be a channel, nickname, inbox or conversation")
}

func (s *Server) searchLocked(client *session, command protocol.Command) {
	target, _ := command.Param(0)
	rawLimit, _ := command.Param(1)
	after, _ := command.Param(2)
	from, _ := command.Param(3)
	rawOptions, _ := command.Param(4) // SEARCH_TERMS; absent from older clients
	limit, err := strconv.Atoi(rawLimit)
	if err != nil || limit < 1 || limit > 1000 || after != "*" && !protocol.ValidMessageID(after) || from != "*" && !protocol.ValidNick(from) || strings.TrimSpace(command.Trailing) == "" || !protocol.BriefText(command.Trailing, 256) {
		s.numericLocked(client, "461", nil, "SEARCH requires target, limit 1-1000, cursor or *, sender or *, and a query of at most 256 bytes")
		return
	}
	options, err := parseSearchOptions(rawOptions)
	if err != nil {
		s.numericLocked(client, "461", nil, "SEARCH options: "+err.Error())
		return
	}
	var body func(string) bool
	if options.terms {
		terms, err := parseSearchTerms(command.Trailing)
		if err != nil {
			s.numericLocked(client, "461", nil, "SEARCH query: "+err.Error())
			return
		}
		body = terms.matches
	} else {
		body = newSearchText(command.Trailing).contains
	}
	selected := target
	if target != "*" {
		selected, err = s.historyTarget(target)
		if err != nil {
			s.numericLocked(client, "430", nil, err.Error())
			return
		}
	}
	accept := func(e *entry) bool {
		return (from == "*" || strings.EqualFold(e.From, from)) && options.inTime(e.Timestamp) && options.matchesRun(e) && body(e.Body)
	}
	var messages []Message
	var status, next string
	var found bool
	if options.newest {
		// The cursor is the oldest match returned so far; continue before it.
		messages, status, next, found = s.readBeforeLocked(selected, after, limit, options.since, accept)
	} else {
		// "*" is the start of the window; an older cursor continues from the archive.
		messages, status, next, found = s.readAfterLocked(selected, after, limit, accept)
	}
	if !found {
		messages, status = nil, historyExpired
	}
	for _, message := range messages {
		s.numericLocked(client, "760", []string{target}, encodeMessage(message))
	}
	params := []string{target, status}
	if status == historyMore {
		params = append(params, next)
	} else if len(messages) > 0 {
		params = append(params, messages[len(messages)-1].ID)
	}
	s.numericLocked(client, "761", params, "End of search")
}
