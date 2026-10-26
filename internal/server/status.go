package server

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/Someblueman/airc/internal/protocol"
)

// A status is an ordinary room message with a shape: the task its author is
// on, whether it is working, blocked or done with it, and where the work is.
// The daemon keeps each nickname's latest status on its activity record, so
// cards and agent listings carry it, until a done status is a day old or the
// daemon restarts; the post itself stays in history like any other.

// statusRequest posts a status in Target (Task, State, optional Link, and a
// note as Text), or lists the latest status of each nickname in Target.
type statusRequest struct {
	Action string
	Target string
	Task   string
	State  string
	Link   string
	Text   string
}

func decodeStatusRequest(_ Config, r protocol.ChatRequest) (statusRequest, error) {
	request := statusRequest{Action: r.Action, Target: r.Target, Task: strings.TrimSpace(r.Task), State: r.State, Link: r.Link, Text: r.Text}
	if !validChannel(r.Target) {
		return statusRequest{}, errors.New("a status is for a room")
	}
	if r.Action == "statuses" {
		return request, nil
	}
	if request.Task == "" || !protocol.BriefText(request.Task, protocol.MaxTaskBytes) {
		return statusRequest{}, fmt.Errorf("a status names its task: one line of at most %d bytes", protocol.MaxTaskBytes)
	}
	if !protocol.ValidStatusState(r.State) {
		return statusRequest{}, errors.New("a status is " + strings.Join(protocol.StatusStates, ", "))
	}
	if r.Link != "" && !protocol.ValidLink(r.Link) {
		return statusRequest{}, fmt.Errorf("a link is an absolute http(s) URL of at most %d bytes", protocol.MaxLinkBytes)
	}
	return request, nil
}

func statusEntry(nick string, status protocol.AgentStatus, meta *protocol.MessageMetadata) protocol.ChatEntry {
	return protocol.ChatEntry{Action: "status", Target: status.Target, From: nick, State: status.State, Text: status.Task, Link: status.Link, At: status.At, Message: meta}
}

// postStatusLocked posts a status and keeps it as the poster's latest.
func (s *Server) postStatusLocked(client *session, r statusRequest) error {
	note := strings.TrimSpace(protocol.NormalizeNewlines(r.Text))
	body, err := s.handoffBody(protocol.StatusText(r.State, r.Task, note), "")
	if err != nil {
		return err
	}
	target, err := s.chatTargetLocked(client, r.Target, "status")
	if err != nil {
		return err
	}
	if !s.slowAllowedLocked(client, target) {
		return errChatDenied
	}
	m := s.postMessage(client, target, body, nil)
	m.Kind, m.AccountID, m.Task, m.State, m.Link = "status", client.accountID, r.Task, r.State, r.Link
	mentions := s.recordLocked(&m)
	s.broadcastMessageLocked(m, client.client.Username, mentions)
	s.receiptLocked(client, m, false)
	status := protocol.AgentStatus{Task: r.Task, State: r.State, Target: target, Link: r.Link, At: m.Timestamp}
	s.noteStatusLocked(client.client.Nick, status)
	meta := messageMetadata(m)
	s.chatEntryLocked(client, statusEntry(client.client.Nick, status, &meta))
	return nil
}

// listStatusesLocked answers the latest status of each nickname that posted
// one in the room, newest first.
func (s *Server) listStatusesLocked(client *session, r statusRequest) error {
	type row struct {
		nick   string
		status protocol.AgentStatus
	}
	var rows []row
	now := s.now()
	for _, a := range s.activity {
		if status := s.agentStatusLocked(a.nick, now); status != nil && status.Target == r.Target {
			rows = append(rows, row{a.nick, *status})
		}
	}
	slices.SortFunc(rows, func(a, b row) int {
		return cmp.Or(b.status.At.Compare(a.status.At), cmp.Compare(nickKey(a.nick), nickKey(b.nick)))
	})
	for _, r := range rows {
		s.chatEntryLocked(client, statusEntry(r.nick, r.status, nil))
	}
	return nil
}
