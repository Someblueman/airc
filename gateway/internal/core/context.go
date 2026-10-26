package core

import "github.com/Someblueman/airc/pkg/irc"

const (
	DefaultContextLimit = 50
	MinContextBytes     = 1024
	MaxContextBytes     = 65536
)

// ContextResult is the bounded, transport-neutral context.read payload. The
// summary stays separate from the retained records so callers can distinguish
// root and omission metadata from returned messages, pins, and profiles.
type ContextResult struct {
	Summary      irc.ContextSummary    `json:"summary"`
	Messages     []irc.MessageMetadata `json:"messages,omitempty"`
	Pins         []irc.MessageMetadata `json:"pins,omitempty"`
	Participants []irc.AgentCard       `json:"participants,omitempty"`
}
