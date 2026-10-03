package irc_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

// handleMessage is the kind of signature an external program needs to write;
// it only compiles if every payload type is nameable through package irc.
func handleMessage(e *irc.MessageEvent) (irc.ChatMetadata, irc.MessageMetadata) {
	return e.ChatMetadata, irc.MessageMetadata{ChatMetadata: e.ChatMetadata, ID: e.ID, Message: e.Message}
}

func TestPayloadTypesNameableFromOutsideModule(t *testing.T) {
	var (
		_ irc.ChatRequest
		_ irc.ChatEntry
		_ irc.ChatMetadata
		_ irc.MessageMetadata
		_ irc.ReceiptInfo
		_ irc.ContextSummary
		_ irc.AgentCard
		_ irc.AgentProfile
		_ irc.CheckRequest
		_ irc.CheckTarget
		_ irc.CheckEntry
		_ irc.AdminRequest
		_ irc.AdminResult
		_ irc.ModerationRule
	)

	delivered := true
	receipt := &irc.ReceiptInfo{Accepted: true, Persisted: true, RecipientConnected: &delivered}
	meta := irc.ChatMetadata{AccountID: "acct", Kind: "note", PollOptions: []string{"a", "b"}}
	send := &irc.SendReceiptEvent{Receipt: receipt, ChatMetadata: meta, ID: "m1"}
	if send.Receipt != receipt || send.MessageEvent().ChatMetadata.AccountID != "acct" {
		t.Fatal("receipt event does not carry the aliased payload types")
	}

	chat, message := handleMessage(&irc.MessageEvent{ChatMetadata: meta, ID: "m2", Message: "hi"})
	if chat.Kind != "note" || message.ID != "m2" || message.AccountID != "acct" {
		t.Fatalf("metadata mismatch: %+v %+v", chat, message)
	}

	card := irc.AgentCard{Nick: "scout", AgentProfile: irc.AgentProfile{Model: "m", About: "a"}, State: "available"}
	entry := irc.ChatEntry{Action: "pin", Message: &message, Profile: &card, Context: &irc.ContextSummary{TriggerID: "m2"}}
	conversation := irc.ConversationContext{ContextSummary: *entry.Context, Messages: []irc.MessageMetadata{message}, Participants: []irc.AgentCard{card}}
	if conversation.TriggerID != "m2" || conversation.Participants[0].Model != "m" {
		t.Fatalf("conversation context mismatch: %+v", conversation)
	}

	check := irc.CheckRequest{Targets: []irc.CheckTarget{{Target: "#room", Limit: 5}}, MaxMessages: 10}
	rule := &irc.ModerationRule{Kind: "mute", Nick: "x", Scope: "#room", SetAt: time.Unix(1, 0)}
	admin := &irc.AdminEvent{AdminResult: irc.AdminResult{Action: "mute", Rule: rule}}
	if check.Targets[0].Target != "#room" || admin.Rule.Nick != "x" {
		t.Fatal("request/result values lost their fields")
	}
	for _, value := range []any{irc.ChatRequest{Action: "list"}, irc.AdminRequest{Action: "list"}, irc.CheckEntry{Kind: "message"}, check, entry, card} {
		if _, err := json.Marshal(value); err != nil {
			t.Fatalf("%T does not encode: %v", value, err)
		}
	}
}
