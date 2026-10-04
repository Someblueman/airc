package backend

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

const (
	maxContextEntries = 1256
	maxContextCount   = 1 << 20
)

// Context reads one retained room conversation without advancing a cursor. It
// first proves the trigger on this exclusive native session, then fetches the
// requested bounded snapshot on that same session.
func (b *Backend) Context(ctx context.Context, grant *store.ReadGrant) (core.ContextResult, error) {
	a, err := grant.Consume(time.Now())
	if err != nil {
		return core.ContextResult{}, err
	}
	if a.Request.Operation != core.ContextRead || a.Request.Target == nil || a.Request.Target.Kind != "room" || !validHistoryID(a.Request.ParentID) || a.Request.Limit < 0 || a.Request.Limit > 1000 || a.Request.MaxBytes < 0 || a.Request.MaxBytes != 0 && a.Request.MaxBytes < core.MinContextBytes || a.Request.MaxBytes > core.MaxContextBytes {
		return core.ContextResult{}, store.ErrInvalid
	}
	limit := a.Request.Limit
	if limit == 0 {
		limit = core.DefaultContextLimit
	}
	maxBytes := a.Request.MaxBytes
	if maxBytes == 0 {
		maxBytes = core.MaxContextBytes
	}
	var result core.ContextResult
	err = b.withRead(ctx, a, false, func(overall context.Context, client *irc.Client) error {
		features := client.Features()
		if features["CHAT"] != "1" || features["CONTEXT"] != "1" || features["CONTEXT_BYTES"] != "1" {
			return ErrReadUnsupported
		}
		proof, err := boundedContextRequest(overall, client, a.Request.ParentID, 1, core.MaxContextBytes)
		if err != nil {
			return err
		}
		root, err := validateContextProof(a.Request, proof)
		if err != nil {
			return err
		}
		snapshot, err := boundedContextRequest(overall, client, a.Request.ParentID, limit, maxBytes)
		if err != nil {
			return err
		}
		if err := validateContextSnapshot(a.Request, root, snapshot); err != nil {
			return err
		}
		result = core.ContextResult{Summary: snapshot.ContextSummary, Messages: snapshot.Messages, Pins: snapshot.Pins, Participants: snapshot.Participants}
		if !contextResultWithinBudget(result) {
			return store.ErrCapacity
		}
		deadline, _ := overall.Deadline()
		if overall.Err() != nil || !time.Now().Before(deadline) {
			return ErrReadUnavailable
		}
		return nil
	})
	if err != nil {
		return core.ContextResult{}, err
	}
	return result, nil
}

func contextResultWithinBudget(result core.ContextResult) bool {
	encoded, err := json.Marshal(result)
	return err == nil && len(encoded) <= core.MaxContextBytes
}

// boundedContextRequest owns the CHAT event stream and bounds retained entries
// independently of the daemon's MaxBytes promise. The SDK event parser already
// caps each IRC line; this collector caps count, aggregate encoded entries and
// rejects malformed/duplicate response structure before returning anything.
func boundedContextRequest(ctx context.Context, client *irc.Client, id string, limit, maxBytes int) (irc.ConversationContext, error) {
	deadline, hasDeadline := ctx.Deadline()
	if err := client.Chat(irc.ChatRequest{Action: "context", ID: id, Limit: limit, MaxBytes: maxBytes}); err != nil {
		return irc.ConversationContext{}, ErrReadUnavailable
	}
	var result irc.ConversationContext
	phase := 0 // messages, then pins, then profiles
	seenSummary := false
	seenMessageIDs := make(map[string]bool)
	seenPinIDs := make(map[string]bool)
	encodedBytes := 0
	entries := 0
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				return irc.ConversationContext{}, ErrReadUnavailable
			}
			switch value := event.(type) {
			case *irc.ChatEvent:
				if seenSummary {
					return irc.ConversationContext{}, ErrReadUnavailable
				}
				entries++
				if entries > maxContextEntries {
					return irc.ConversationContext{}, store.ErrCapacity
				}
				line, err := json.Marshal(value.ChatEntry)
				wireBytes := contextEntryWireBytes(client, line)
				if err != nil || wireBytes > maxBytes || encodedBytes > maxBytes-wireBytes {
					return irc.ConversationContext{}, store.ErrCapacity
				}
				switch value.Action {
				case "context":
					if seenSummary || value.Context == nil || value.Message != nil || value.Profile != nil || !chatEntryBare(value.ChatEntry) {
						return irc.ConversationContext{}, ErrReadUnavailable
					}
					result.ContextSummary = *value.Context
					if !validContextSummary(result) {
						return irc.ConversationContext{}, ErrReadUnavailable
					}
					seenSummary = true
				case "context-message":
					if phase > 0 || value.Message == nil || value.Profile != nil || value.Context != nil || !chatEntryBare(value.ChatEntry) {
						return irc.ConversationContext{}, ErrReadUnavailable
					}
					if len(result.Messages) >= limit || !validContextMessage(*value.Message, "") || seenMessageIDs[value.Message.ID] {
						return irc.ConversationContext{}, ErrReadUnavailable
					}
					seenMessageIDs[value.Message.ID] = true
					result.Messages = append(result.Messages, *value.Message)
				case "context-pin":
					if phase > 1 || value.Message == nil || value.Profile != nil || value.Context != nil || !chatEntryBare(value.ChatEntry) {
						return irc.ConversationContext{}, ErrReadUnavailable
					}
					phase = 1
					if len(result.Pins) >= 5 || !validContextMessage(*value.Message, "") || seenPinIDs[value.Message.ID] {
						return irc.ConversationContext{}, ErrReadUnavailable
					}
					seenPinIDs[value.Message.ID] = true
					result.Pins = append(result.Pins, *value.Message)
				case "context-profile":
					if value.Profile == nil || value.Context != nil || value.Message != nil || !chatEntryBare(value.ChatEntry) {
						return irc.ConversationContext{}, ErrReadUnavailable
					}
					if len(result.Participants) >= 64 || !validCard(*value.Profile) || !validNick(value.Profile.Nick) {
						return irc.ConversationContext{}, ErrReadUnavailable
					}
					key := strings.ToLower(value.Profile.Nick)
					for _, prior := range result.Participants {
						if strings.EqualFold(prior.Nick, key) {
							return irc.ConversationContext{}, ErrReadUnavailable
						}
					}
					phase = 2
					result.Participants = append(result.Participants, *value.Profile)
				default:
					return irc.ConversationContext{}, ErrReadUnavailable
				}
				encodedBytes += wireBytes
			case *irc.EndOfChatEvent:
				if !seenSummary || ctx.Err() != nil || hasDeadline && !time.Now().Before(deadline) {
					return irc.ConversationContext{}, ErrReadUnavailable
				}
				if encodedBytes+contextEndWireBytes(client) > maxBytes {
					return irc.ConversationContext{}, store.ErrCapacity
				}
				return result, nil
			case *irc.RawEvent:
				if value.Command == "777" || value.Command == "778" {
					return irc.ConversationContext{}, ErrReadUnavailable
				}
				if value.Command != "422" && len(value.Command) == 3 && value.Command[0] >= '4' && value.Command[0] <= '5' {
					return irc.ConversationContext{}, auth.ErrForbidden
				}
			}
		case <-ctx.Done():
			return irc.ConversationContext{}, ErrReadUnavailable
		}
	}
}

func contextEntryWireBytes(client *irc.Client, encoded []byte) int {
	return len(protocol.Format("server", "777", []string{client.Nick()}, string(encoded)))
}

func contextEndWireBytes(client *irc.Client) int {
	return len(protocol.Format("server", "778", []string{client.Nick()}, "End of chat response"))
}

func chatEntryBare(entry irc.ChatEntry) bool {
	return entry.Target == "" && entry.ID == "" && entry.From == "" && entry.Text == "" && entry.ExpiresAt.IsZero() && entry.SlowSeconds == 0 && entry.HistoryLimit == 0 && len(entry.Options) == 0 && len(entry.Votes) == 0 && !entry.Closed
}

func validateContextProof(r core.Request, c irc.ConversationContext) (string, error) {
	if r.Target == nil || r.Target.Kind != "room" || !validContextSummary(c) || len(c.Missing) > 2 || c.TriggerID != r.ParentID || len(c.Messages) != 1 || c.Messages[0].ID != r.ParentID || containsString(c.Missing, r.ParentID) {
		return "", auth.ErrForbidden
	}
	parent := c.Messages[0]
	if !validContextMessage(parent, r.Target.Name) {
		return "", auth.ErrForbidden
	}
	root := parent.ID
	if parent.ThreadID != "" {
		root = parent.ThreadID
	}
	if parent.ID == c.RootID && parent.ThreadID != "" || containsString(c.Missing, c.RootID) && c.RootID == parent.ID {
		return "", auth.ErrForbidden
	}
	if !validHistoryID(c.RootID) || c.RootID != root {
		return "", auth.ErrForbidden
	}
	if err := validateContextExtras(c, map[string]irc.MessageMetadata{parent.ID: parent}, r.Target.Name); err != nil {
		return "", err
	}
	return root, nil
}

func validateContextSnapshot(r core.Request, root string, c irc.ConversationContext) error {
	if r.Target == nil || r.Target.Kind != "room" || c.TriggerID != r.ParentID || c.RootID != root || containsString(c.Missing, r.ParentID) || !validContextSummary(c) {
		return auth.ErrForbidden
	}
	found := false
	messageIDs := make(map[string]bool, len(c.Messages))
	messageByID := make(map[string]irc.MessageMetadata, len(c.Messages))
	for _, message := range c.Messages {
		if !validContextMessage(message, r.Target.Name) || !validContextThreadMessage(message, root) {
			return auth.ErrForbidden
		}
		if message.ID == r.ParentID {
			found = true
		}
		messageIDs[message.ID] = true
		messageByID[message.ID] = message
	}
	if !found {
		return auth.ErrForbidden
	}
	if containsString(c.Missing, root) && messageIDs[root] {
		return auth.ErrForbidden
	}
	if err := validateContextExtras(c, messageByID, r.Target.Name); err != nil {
		return err
	}
	return nil
}

func validateContextExtras(c irc.ConversationContext, messageByID map[string]irc.MessageMetadata, target string) error {
	if len(c.ProtectedIDs) > 4 {
		return auth.ErrForbidden
	}
	if len(c.Pins) > 5 {
		return auth.ErrForbidden
	}
	seenProfiles := make(map[string]bool, len(c.Participants))
	if len(c.Participants) > 64 {
		return auth.ErrForbidden
	}
	for _, pin := range c.Pins {
		if !validContextMessage(pin, target) {
			return auth.ErrForbidden
		}
		if message, found := messageByID[pin.ID]; found && !contextMetadataEqual(message, pin) {
			return auth.ErrForbidden
		}
	}
	for _, profile := range c.Participants {
		key := strings.ToLower(profile.Nick)
		if !validCard(profile) || !validNick(profile.Nick) || seenProfiles[key] {
			return auth.ErrForbidden
		}
		seenProfiles[key] = true
	}
	if messageByID != nil {
		for _, id := range c.ProtectedIDs {
			if _, found := messageByID[id]; !found {
				return auth.ErrForbidden
			}
		}
	}
	return nil
}

func validContextThreadMessage(message irc.MessageMetadata, root string) bool {
	if message.ID == root {
		return message.ThreadID == ""
	}
	return message.ThreadID == root
}

func contextMetadataEqual(left, right irc.MessageMetadata) bool {
	a, errA := json.Marshal(left)
	b, errB := json.Marshal(right)
	return errA == nil && errB == nil && string(a) == string(b)
}

func validContextMessage(message irc.MessageMetadata, target string) bool {
	if !validHistoryID(message.ID) || target != "" && message.Target != target || !validRoom(message.Target) || !utf8.ValidString(message.Message) || strings.ContainsRune(message.Message, 0) || len(message.Message) > 4096 || message.Timestamp.IsZero() {
		return false
	}
	from := core.Target{Kind: "direct", Name: message.From}
	if from.Normalize() != nil || !protocol.BriefText(message.Kind, 32) || message.AccountID != "" && !validHistoryID(message.AccountID) || message.RequestID != "" && !protocol.ValidRequestID(message.RequestID) || message.ReplyTo != "" && !validHistoryID(message.ReplyTo) || message.ThreadID != "" && !validHistoryID(message.ThreadID) || message.Supersedes != "" && !validHistoryID(message.Supersedes) || message.SupersededBy != "" && !validHistoryID(message.SupersededBy) || message.Reaction != "" && !protocol.ValidReaction(message.Reaction) || len(message.PollOptions) > 8 {
		return false
	}
	for _, option := range message.PollOptions {
		if !protocol.BriefText(option, 80) {
			return false
		}
	}
	return true
}

func validRoom(room string) bool {
	target := core.Target{Kind: "room", Name: room}
	return target.Normalize() == nil
}

func validNick(nick string) bool {
	target := core.Target{Kind: "direct", Name: nick}
	return target.Normalize() == nil
}

func validContextSummary(c irc.ConversationContext) bool {
	if !validHistoryID(c.TriggerID) || !validHistoryID(c.RootID) || len(c.ProtectedIDs) > 4 || c.OmittedMessages < 0 || c.OmittedMessages > maxContextCount || c.OmittedPins < 0 || c.OmittedPins > maxContextCount || c.OmittedProfiles < 0 || c.OmittedProfiles > maxContextCount {
		return false
	}
	seen := make(map[string]bool)
	for _, id := range append(append([]string{}, c.Missing...), c.ProtectedIDs...) {
		if !validHistoryID(id) || seen[id] {
			return false
		}
		seen[id] = true
	}
	for _, id := range c.Missing {
		if id != c.TriggerID && id != c.RootID {
			return false
		}
	}
	for _, id := range c.ProtectedIDs {
		if containsString(c.Missing, id) {
			return false
		}
	}
	return true
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
