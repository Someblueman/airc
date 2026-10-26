package protocol

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"
)

const ChatTag = "+airc/chat"
const RequestIDTag = "+airc/request-id"

type ChatMetadata struct {
	AccountID string `json:"account_id,omitempty"`
	// RecipientAccountID is, on a direct message to a registered nickname,
	// the account that owned the nickname when the message was stored: the
	// proof of who received it once the nickname changes hands.
	RecipientAccountID string    `json:"recipient_account_id,omitempty"`
	RequestID          string    `json:"request_id,omitempty"`
	Kind               string    `json:"kind,omitempty"`
	Supersedes         string    `json:"supersedes,omitempty"`
	SupersededBy       string    `json:"superseded_by,omitempty"`
	Retracted          bool      `json:"retracted,omitempty"`
	PollOptions        []string  `json:"poll_options,omitempty"`
	PollClosesAt       time.Time `json:"poll_closes_at,omitzero"`
	// For names who a handoff or decision step is addressed to: on an
	// offer, the nick it is handed to or who decides; on accept, blocked,
	// decline, done and an answer, the offer's author. A room message
	// counts as a mention of it.
	For string `json:"for,omitempty"`
	// Link is where a handoff's result is, on the done step that recorded
	// it: an absolute http(s) URL (ValidLink) of a PR, a commit or a document.
	Link string `json:"link,omitempty"`
	// Choices are a decision's options, numbered from 1; Choice is the one
	// an answer chose, 0 when it gave only a note.
	Choices []string `json:"choices,omitempty"`
	Choice  int      `json:"choice,omitempty"`
	// Paste describes the paste a message shares (PasteTag), whose
	// reference line ends the body.
	Paste *PasteInfo `json:"paste,omitempty"`
	// Task and State are a status post's shape: what its author is on and
	// whether it is working, blocked or done with it; Link, where the work
	// is, when given.
	Task  string `json:"task,omitempty"`
	State string `json:"state,omitempty"`
	// Resolved marks a thread root that has a retained reply of kind
	// resolved: what it asked is settled, wherever that happened.
	Resolved bool `json:"resolved,omitempty"`
}

// StatusStates are the states a status post may be in.
var StatusStates = []string{"working", "blocked", "done"}

// MaxTaskBytes bounds a status post's task.
const MaxTaskBytes = 200

// ValidStatusState reports whether state is one of StatusStates.
func ValidStatusState(state string) bool {
	return slices.Contains(StatusStates, state)
}

// StatusText is the body a status post carries, readable as it stands by a
// client that knows no statuses: "Status [working] task", then ": note"
// when a note was given.
func StatusText(state, task, note string) string {
	text := "Status [" + state + "] " + task
	if note != "" {
		text += ": " + note
	}
	return text
}

// ResolvedText is the body a resolution carries: the note, else "Resolved".
func ResolvedText(note string) string {
	if note != "" {
		return note
	}
	return "Resolved"
}

// DecisionStepText is the body a decision step carries. An answer names its
// choice for a reader that knows no decisions ("Decided 2 (option)", then
// ": note" when a note was given), or is the note alone when the decision
// had no options or no option was chosen; a withdrawal without a note reads
// "Withdrew decision ID".
func DecisionStepText(kind, offer string, choice int, option, note string) string {
	switch kind {
	case "decision-answer":
		if option == "" {
			return note
		}
		text := fmt.Sprintf("Decided %d (%s)", choice, option)
		if note != "" {
			text += ": " + note
		}
		return text
	case "decision-withdraw":
		if note != "" {
			return note
		}
		return "Withdrew decision " + offer
	}
	return ""
}

// HandoffStepText is the body a handoff step carries when it has no note
// of its own, readable as it stands by a client that knows no handoffs.
// detail is what the step records besides a note: a blocked step's reason
// ("Blocked handoff ID: reason"), which it always carries, or a done step's
// link ("Finished handoff ID: URL"). "handoff-resume" is an accept that
// takes a blocked handoff up again ("Resumed handoff ID").
func HandoffStepText(kind, offer, detail string) string {
	text := ""
	switch kind {
	case "handoff-accept":
		text = "Accepted handoff " + offer
	case "handoff-resume":
		text = "Resumed handoff " + offer
	case "handoff-blocked":
		text = "Blocked handoff " + offer
	case "handoff-decline":
		text = "Declined handoff " + offer
	case "handoff-done":
		text = "Done with handoff " + offer
		if detail != "" {
			text = "Finished handoff " + offer
		}
	case "handoff-withdraw":
		text = "Withdrew handoff " + offer
	}
	if text != "" && detail != "" {
		text += ": " + detail
	}
	return text
}

// PlainHandoffStep reports whether body is what a step of kind on offer
// reads without a note of its own, whatever detail it names, so a client
// that marks steps need not mark it.
func PlainHandoffStep(kind, offer, body string) bool {
	kinds := []string{kind}
	if kind == "handoff-accept" {
		kinds = append(kinds, "handoff-resume")
	}
	for _, k := range kinds {
		if text := HandoffStepText(k, offer, ""); text != "" && (body == text || strings.HasPrefix(body, text+": ")) {
			return true
		}
	}
	return kind == "handoff-done" && strings.HasPrefix(body, "Finished handoff "+offer+": ")
}

// MaxLinkBytes bounds the link a done step records.
const MaxLinkBytes = 400

// ValidLink reports a link a done step may record: an absolute http or
// https URL of at most MaxLinkBytes bytes with a host, without credentials
// or whitespace, written as the URL parser writes it back.
func ValidLink(link string) bool {
	if link == "" || len(link) > MaxLinkBytes || strings.IndexFunc(link, unicode.IsSpace) >= 0 {
		return false
	}
	u, err := url.Parse(link)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil && u.String() == link
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
	For      string   `json:"for,omitempty"`
	// Link is where a finished handoff's result is, on handoff-done, or
	// where a status post's work is.
	Link string `json:"link,omitempty"`
	// Pastes: paste-begin names the upload (Name, Language, Size, Lines)
	// under a client-chosen Token; paste-chunk adds Data, base64 of at most
	// PasteChunkBytes, as chunk Seq of that Token; paste-end finishes it;
	// paste-get reads paste ID from Offset, at most Limit entries.
	Name     string `json:"name,omitempty"`
	Language string `json:"language,omitempty"`
	Size     int    `json:"size,omitempty"`
	Lines    int    `json:"lines,omitempty"`
	Token    string `json:"token,omitempty"`
	Seq      int    `json:"seq,omitempty"`
	Data     string `json:"data,omitempty"`
	Offset   int    `json:"offset,omitempty"`
	// Task and State are a status post's.
	Task  string `json:"task,omitempty"`
	State string `json:"state,omitempty"`
}

type ChatEntry struct {
	Context      *ContextSummary `json:"context,omitempty"`
	Profile      *AgentCard      `json:"profile,omitempty"`
	Action       string          `json:"action"`
	Target       string          `json:"target,omitempty"`
	ID           string          `json:"id,omitempty"`
	From         string          `json:"from,omitempty"`
	AccountID    string          `json:"account_id,omitempty"`
	Text         string          `json:"text,omitempty"`
	ExpiresAt    time.Time       `json:"expires_at,omitzero"`
	SlowSeconds  int64           `json:"slow_seconds,omitempty"`
	HistoryLimit int             `json:"history_limit,omitempty"`
	Options      []string        `json:"options,omitempty"`
	Votes        []int           `json:"votes,omitempty"`
	Closed       bool            `json:"closed,omitempty"`
	State        string          `json:"state,omitempty"`
	Holder       string          `json:"holder,omitempty"`
	// Link is where a done handoff's result is; BlockedReason and BlockedAt
	// are why and since when a blocked handoff is stuck.
	Link          string           `json:"link,omitempty"`
	BlockedReason string           `json:"blocked_reason,omitempty"`
	BlockedAt     time.Time        `json:"blocked_at,omitzero"`
	Message       *MessageMetadata `json:"message,omitempty"`
	// Answer is a decision's answer message once it has one.
	Answer *MessageMetadata `json:"answer,omitempty"`
	// Pastes: Token acknowledges an upload step; Paste, Created and
	// Referenced describe a stored paste, with From and AccountID its
	// author; Data is base64 of the paste's bytes from Offset.
	Token      string     `json:"token,omitempty"`
	Paste      *PasteInfo `json:"paste,omitempty"`
	Created    time.Time  `json:"created,omitzero"`
	Referenced time.Time  `json:"referenced,omitzero"`
	Data       string     `json:"data,omitempty"`
	Offset     int        `json:"offset,omitempty"`
	// At is when a status entry's status was posted.
	At time.Time `json:"at,omitzero"`
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
