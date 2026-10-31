package server

import (
	"errors"
	"slices"
	"strings"

	"github.com/Someblueman/airc/internal/protocol"
)

// A resolution closes a question whose answer arrived some other way: in a
// direct message, by a reaction, or in person. It is an ordinary reply to
// the root of the message's thread with kind resolved, tagging nobody, and
// the ring marks the root resolved while the reply is retained, so a digest
// stops waiting on it. There is no state of its own: a repeat finds the
// earlier resolution among the root's replies.

// resolveRequest resolves the thread of message ID, with a note as Text.
type resolveRequest struct {
	ID   string
	Text string
}

func decodeResolveRequest(_ Config, r protocol.ChatRequest) (resolveRequest, error) {
	if !protocol.ValidIDArgument(r.ID) {
		return resolveRequest{}, errors.New("resolve names the message whose thread is settled")
	}
	return resolveRequest{ID: r.ID, Text: r.Text}, nil
}

// mayResolveLocked reports whether client may resolve root: its author, who
// it is addressed to (its for, a nickname it mentions, or the recipient of
// a direct message) or an admin.
func (s *Server) mayResolveLocked(client *session, root Message) bool {
	if ownsMessage(client, root) {
		return true
	}
	addressed := protocol.MessageMentions(root.Body, root.For, root.GroupMentions)
	if !protocol.IsChannel(root.Target) {
		addressed = append(addressed, root.Target)
	}
	return slices.ContainsFunc(addressed, func(nick string) bool { return s.isNickLocked(client, nick) })
}

func (s *Server) resolveLocked(client *session, r resolveRequest) error {
	m, found := s.history.message(r.ID)
	if !found {
		return errors.New("message is not retained")
	}
	root := m
	if m.ThreadID != "" {
		if root, found = s.history.message(m.ThreadID); !found {
			return errors.New("the thread's root is no longer retained")
		}
	}
	if !s.mayResolveLocked(client, root) {
		return errors.New("only the author, who it is addressed to, or an admin may resolve this")
	}
	// A resolution repeated after its answer was lost answers again, unposted.
	var earlier *Message
	s.history.forward(s.history.selectTarget("replies:"+root.ID), 0, func(_ uint64, e *entry) bool {
		if e.Kind == "resolved" && authored(client, e.Message) {
			earlier = &e.Message
		}
		return earlier == nil
	})
	if earlier != nil {
		meta := messageMetadata(*earlier)
		s.chatEntryLocked(client, protocol.ChatEntry{Action: "resolve", Target: earlier.Target, ID: root.ID, From: earlier.From, Message: &meta})
		return nil
	}
	body, err := s.chatBody(r.Text, protocol.ResolvedText(""))
	if err != nil {
		return err
	}
	target := root.Target
	if !protocol.IsChannel(target) && !strings.EqualFold(client.client.Nick, root.From) {
		// A direct conversation's resolution goes to the other side.
		target = root.From
	}
	if target, err = s.chatTargetLocked(client, target, "resolution"); err != nil {
		return err
	}
	if !s.slowAllowedLocked(client, target) {
		return errChatDenied
	}
	reply := s.postMessage(client, target, body, &root)
	reply.Kind, reply.AccountID = "resolved", client.accountID
	s.postStructuredLocked(client, reply)
	meta := messageMetadata(reply)
	s.chatEntryLocked(client, protocol.ChatEntry{Action: "resolve", Target: reply.Target, ID: root.ID, From: reply.From, Message: &meta})
	return nil
}
