package protocol

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
	"unicode"
)

const ChatTag = "+airc/chat"
const RequestIDTag = "+airc/request-id"

type ChatMetadata struct {
	AccountID    string    `json:"account_id,omitempty"`
	RequestID    string    `json:"request_id,omitempty"`
	Kind         string    `json:"kind,omitempty"`
	Supersedes   string    `json:"supersedes,omitempty"`
	SupersededBy string    `json:"superseded_by,omitempty"`
	Retracted    bool      `json:"retracted,omitempty"`
	PollOptions  []string  `json:"poll_options,omitempty"`
	PollClosesAt time.Time `json:"poll_closes_at,omitzero"`
}

func EncodeChat(meta ChatMetadata) string {
	data, _ := json.Marshal(meta)
	return base64.RawURLEncoding.EncodeToString(data)
}
func DecodeChat(encoded string) ChatMetadata {
	var meta ChatMetadata
	if encoded == "" {
		return meta // most messages carry no chat tag
	}
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err == nil {
		_ = json.Unmarshal(data, &meta)
	}
	return meta
}

func ValidRequestID(id string) bool {
	if len(id) < 1 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if c < 'a' || c > 'z' {
			if c < 'A' || c > 'Z' {
				if c < '0' || c > '9' {
					if c != '-' && c != '_' {
						return false
					}
				}
			}
		}
	}
	return true
}

type ChatRequest struct {
	Action   string   `json:"action"`
	Target   string   `json:"target,omitempty"`
	ID       string   `json:"id,omitempty"`
	Text     string   `json:"text,omitempty"`
	Seconds  int64    `json:"seconds,omitempty"`
	Limit    int      `json:"limit,omitempty"`
	MaxBytes int      `json:"max_bytes,omitempty"`
	Options  []string `json:"options,omitempty"`
	Choice   int      `json:"choice,omitempty"`
}

type ChatEntry struct {
	Context      *ContextSummary  `json:"context,omitempty"`
	Profile      *AgentCard       `json:"profile,omitempty"`
	Action       string           `json:"action"`
	Target       string           `json:"target,omitempty"`
	ID           string           `json:"id,omitempty"`
	From         string           `json:"from,omitempty"`
	AccountID    string           `json:"account_id,omitempty"`
	Text         string           `json:"text,omitempty"`
	ExpiresAt    time.Time        `json:"expires_at,omitzero"`
	SlowSeconds  int64            `json:"slow_seconds,omitempty"`
	HistoryLimit int              `json:"history_limit,omitempty"`
	Options      []string         `json:"options,omitempty"`
	Votes        []int            `json:"votes,omitempty"`
	Closed       bool             `json:"closed,omitempty"`
	Message      *MessageMetadata `json:"message,omitempty"`
}

func CustomReaction(kind string) bool {
	return BriefText(kind, 32) && strings.TrimSpace(kind) == kind && kind != "" && strings.IndexFunc(kind, unicode.IsSpace) < 0
}

// Counts cover retained content omitted by limits. Missing identifies evicted
// trigger/root messages; the number of older evicted replies is unknowable.
// ProtectedIDs identifies records local trimming must retain when intermediate
// correction links are omitted. It is sent with negotiated byte budgets.
type ContextSummary struct {
	TriggerID       string   `json:"trigger_id"`
	RootID          string   `json:"root_id"`
	Missing         []string `json:"missing,omitempty"`
	ProtectedIDs    []string `json:"protected_ids,omitempty"`
	OmittedMessages int      `json:"omitted_messages"`
	OmittedPins     int      `json:"omitted_pins"`
	OmittedProfiles int      `json:"omitted_profiles"`
}
