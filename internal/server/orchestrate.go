package server

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
)

// A room is orchestrated when its settings name an orchestrator: a
// registered nickname that alone (with an admin) hands work out in the
// room, beside a one-line goal. Only an admin sets either. Turning the mode
// on, changing either field or turning it off posts one retained @agents
// message of kind orchestrate in the room, so every agent is tagged; the
// charter CHECK entry carries both fields, and a 780 signal tells open UIs.
// A done step on a handoff in the room must say what was delivered.

// registeredLocked reports whether nick has an account.
func (s *Server) registeredLocked(nick string) bool {
	_, registered := s.accounts[nickKey(nick)]
	return registered
}

func errOrchestratorUnregistered(nick string) error {
	return fmt.Errorf("the orchestrator must be a registered nickname; airc user create --nick %s", nick)
}

// orchestratesLocked reports whether client is the orchestrator of target,
// by account: the orchestrator is always a registered nickname, so a guest
// on its name never is.
func (s *Server) orchestratesLocked(client *session, target string) bool {
	nick := s.chat.Rooms[target].Orchestrator
	if nick == "" || client.accountID == "" {
		return false
	}
	a, registered := s.accounts[nickKey(nick)]
	return registered && a.ID == client.accountID
}

// orchestrateText is the announcement of a room's mode as it now is.
func orchestrateText(target string, room roomSettings) string {
	if room.Orchestrator == "" {
		return "@" + protocol.GroupAgents + " " + target + " is no longer orchestrated."
	}
	goal := "."
	if room.Goal != "" {
		goal = ": " + room.Goal
		if !strings.ContainsAny(goal[len(goal)-1:], ".!?") {
			goal += "."
		}
	}
	return fmt.Sprintf("@%s %s orchestrates %s%s Take %s's handoffs, post status at changes, finish with a note or a link.", protocol.GroupAgents, room.Orchestrator, target, goal, room.Orchestrator)
}

// orchestrateLocked applies an orchestrator and goal request to room, as
// the room action's admin asked: a nil field is left, an empty orchestrator
// clears both, and an orchestrator must be a registered nickname, kept in
// its account's spelling.
func (s *Server) orchestrateLocked(room *roomSettings, orchestrator, goal *string) error {
	if orchestrator != nil {
		room.Orchestrator = ""
		if *orchestrator != "" {
			a, registered := s.accounts[nickKey(*orchestrator)]
			if !registered {
				return errOrchestratorUnregistered(*orchestrator)
			}
			room.Orchestrator = a.Nick
		}
	}
	if goal != nil {
		room.Goal = *goal
	}
	if room.Orchestrator == "" {
		if goal != nil && *goal != "" {
			return errors.New("a goal goes with an orchestrator; set one with the goal")
		}
		room.Goal = ""
	}
	return nil
}

// announceOrchestrateLocked posts the announcement of target's mode from
// client, wherever client is (an admin need not join the room), and signals
// it to the room's open clients.
func (s *Server) announceOrchestrateLocked(client *session, target, body string) {
	room := s.chat.Rooms[target]
	m := s.postMessage(client, target, body, nil)
	m.Kind, m.AccountID = "orchestrate", client.accountID
	s.postStructuredLocked(client, m)
	s.broadcastSignalLocked(client, protocol.ChatEntry{Action: "orchestrate", Target: target, From: client.client.Nick, Orchestrator: room.Orchestrator, Goal: room.Goal, ExpiresAt: s.now().UTC().Add(time.Minute)}, Message{})
}

// clearOrchestratorLocked ends the mode in every room nick orchestrates,
// before its account is deleted: a load refuses an orchestrator without an
// account.
func (s *Server) clearOrchestratorLocked(nick string) error {
	next, changed := s.copyChat(), false
	for target, room := range next.Rooms {
		if room.Orchestrator != "" && nickKey(room.Orchestrator) == nickKey(nick) {
			room.Orchestrator, room.Goal = "", ""
			if room == (roomSettings{}) {
				delete(next.Rooms, target)
			} else {
				next.Rooms[target] = room
			}
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return s.saveChatLocked(next)
}
