package server

import (
	"errors"
	"strings"

	"github.com/Someblueman/airc/internal/protocol"
)

// An ID argument may abbreviate a message, handoff, decision or paste ID to
// its first protocol.MinIDPrefix or more characters, so a person or an agent
// types `airc thread 3a13b80f` rather than the 32 characters the daemon
// emits. The daemon resolves it where the argument is read, among what it
// retains: a message ID in the history window (a thread root or reply parent
// whose descendants are retained included), the pins, the polls and the
// handoff and decision ledgers; a paste ID in the paste index. The archive
// is not searched: nothing that takes an ID reaches an archived message. An
// abbreviation two retained IDs match is refused as a bad argument (461);
// one none matches is passed on as given, so the command's own lookup
// answers "not retained" as it does for any unknown ID.

// idStore names what kind of ID an argument abbreviates.
type idStore int

const (
	messageIDStore idStore = iota + 1 // messages, handoffs, decisions and polls: all messages
	pasteIDStore
)

// ambiguousIDError refuses an abbreviation that more than one retained ID
// matches.
type ambiguousIDError struct{ prefix string }

func (e ambiguousIDError) Error() string {
	return "ambiguous ID prefix " + e.prefix + "; give more of it"
}

// resolveIDLocked returns the retained ID that arg abbreviates. A full ID,
// an argument of another shape and an abbreviation nothing matches are
// returned as they are.
func (s *Server) resolveIDLocked(arg string, store idStore) (string, error) {
	if protocol.ValidMessageID(arg) || !protocol.ValidIDArgument(arg) {
		return arg, nil
	}
	found := ""
	// match notes an ID with the prefix and reports whether it is a second one.
	match := func(id string) bool {
		if !strings.HasPrefix(id, arg) || id == found {
			return false
		}
		if found != "" {
			return true
		}
		found = id
		return false
	}
	ambiguous := false
	switch store {
	case messageIDStore:
		ambiguous = s.history.idsWithPrefix(arg, match)
		for _, pins := range s.chat.Pins {
			for _, pin := range pins {
				ambiguous = ambiguous || match(pin.ID)
			}
		}
		for id := range s.chat.Polls {
			ambiguous = ambiguous || match(id)
		}
		for id := range s.handoffs.all {
			ambiguous = ambiguous || match(id)
		}
		for id := range s.decisions.all {
			ambiguous = ambiguous || match(id)
		}
	case pasteIDStore:
		for id := range s.pastes {
			ambiguous = ambiguous || match(id)
		}
	}
	if ambiguous {
		return "", ambiguousIDError{prefix: arg}
	}
	if found == "" {
		return arg, nil
	}
	return found, nil
}

// idsWithPrefix calls visit with each retained ID that starts with prefix,
// the window's messages and the roots and parents its index knows (a thread
// root evicted while its replies are retained), until visit returns true.
// It scans the window's IDs: an abbreviated ID is a convenience for a
// person or an agent typing one, not a hot path.
func (h *historyRing) idsWithPrefix(prefix string, visit func(id string) bool) bool {
	for id := range h.positions {
		if strings.HasPrefix(id, prefix) && visit(id) {
			return true
		}
	}
	for key := range h.index {
		id, found := strings.CutPrefix(key, "thread:")
		if !found {
			id, found = strings.CutPrefix(key, "child:")
		}
		if found && strings.HasPrefix(id, prefix) && visit(id) {
			return true
		}
	}
	return false
}

// conversationLocked is the ring's conversation with the selector's ID
// resolved first, so thread:3a13b80f names the retained thread.
func (s *Server) conversationLocked(target string) (string, error) {
	kind, id, found := strings.Cut(target, ":")
	if found && (kind == "thread" || kind == "replies") {
		resolved, err := s.resolveIDLocked(id, messageIDStore)
		if err != nil {
			return "", err
		}
		target = kind + ":" + resolved
	}
	return s.history.conversation(target)
}

// conversationErrorLocked answers a conversation selector's error: an
// ambiguous abbreviation is a bad argument, and anything else is a
// conversation that is not retained.
func (s *Server) conversationErrorLocked(client *session, params []string, err error) {
	if errors.As(err, new(ambiguousIDError)) {
		s.numericLocked(client, "461", params, err.Error())
		return
	}
	s.numericLocked(client, "430", params, err.Error())
}
