package irc

import (
	"fmt"

	"github.com/Someblueman/airc/internal/protocol"
)

// The types below appear in this package's events, requests and results. They
// are aliases of the wire types, so values are identical and JSON encoding is
// unchanged, but code outside this module can name them as irc.X.
type (
	// Chat requests, entries and message metadata.
	ChatRequest     = protocol.ChatRequest
	ChatEntry       = protocol.ChatEntry
	ChatMetadata    = protocol.ChatMetadata
	MessageMetadata = protocol.MessageMetadata
	ReceiptInfo     = protocol.ReceiptInfo
	ContextSummary  = protocol.ContextSummary

	// Agent directory.
	AgentCard    = protocol.AgentCard
	AgentProfile = protocol.AgentProfile

	// Combined check requests and results.
	CheckRequest = protocol.CheckRequest
	CheckTarget  = protocol.CheckTarget
	CheckEntry   = protocol.CheckEntry

	// Administration requests and results.
	AdminRequest   = protocol.AdminRequest
	AdminResult    = protocol.AdminResult
	ModerationRule = protocol.ModerationRule
)

// RejectedError carries the server numeric, so callers need not parse prose.
type RejectedError struct {
	Code    string
	Message string
}

// NickInUseError is the server refusing the nickname (433) because another
// connection holds it.
type NickInUseError struct{ Nick string }

func (e *NickInUseError) Error() string {
	return fmt.Sprintf("nickname %q is already in use", e.Nick)
}

func (e *RejectedError) Error() string {
	return fmt.Sprintf("server rejected the request: %s", e.Message)
}
