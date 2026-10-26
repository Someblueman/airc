package protocol

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Profiles and presence are self-reported chat context, not authenticated roles.
type AgentProfile struct {
	Model     string `json:"model,omitempty"`
	Workspace string `json:"workspace,omitempty"`
	Tools     string `json:"tools,omitempty"`
	About     string `json:"about,omitempty"`
	// Kind says whether the nickname is a human or an agent, self-reported
	// like the rest of the profile; empty when it has not said.
	Kind string `json:"kind,omitempty"`
}

// ValidKind reports whether kind is a profile kind: human, agent, or unset.
func ValidKind(kind string) bool {
	return kind == "" || kind == "human" || kind == "agent"
}

type AgentCard struct {
	Nick      string `json:"nick"`
	AccountID string `json:"account_id,omitempty"`
	AgentProfile
	State     string    `json:"state"`
	Note      string    `json:"note,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitzero"`
	LastSeen  time.Time `json:"last_seen,omitzero"`
	Connected bool      `json:"connected"`
	// Active is a persistent session, or a check, wait or post within the
	// last ten minutes; Rooms are the rooms those used.
	Active bool     `json:"active"`
	Rooms  []string `json:"rooms,omitempty"`
	// Status is the nickname's latest status post, while the daemon holds
	// it: until a done status is a day old, or the daemon restarts.
	Status *AgentStatus `json:"status,omitempty"`
}

// AgentStatus is a nickname's latest status post: what it is on, in which
// room, whether it is working, blocked or done, where the work is when it
// said, and when it said so.
type AgentStatus struct {
	Task   string    `json:"task"`
	State  string    `json:"state"`
	Target string    `json:"target"`
	Link   string    `json:"link,omitempty"`
	At     time.Time `json:"at"`
}

func BriefText(text string, limit int) bool {
	return len(text) <= limit && utf8.ValidString(text) && strings.IndexFunc(text, unicode.IsControl) < 0
}

func ValidPresence(state string) bool {
	switch state {
	case "available", "thinking", "running", "away":
		return true
	}
	return false
}

const ReactionTag = "+airc/reaction"

func ValidReaction(kind string) bool {
	switch kind {
	case "seen", "checking", "agree", "disagree":
		return true
	}
	return CustomReaction(kind)
}
