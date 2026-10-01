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
}

type AgentCard struct {
	Nick string `json:"nick"`
	AgentProfile
	State     string    `json:"state"`
	Note      string    `json:"note,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitzero"`
	LastSeen  time.Time `json:"last_seen,omitzero"`
	Connected bool      `json:"connected"`
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
	return false
}
