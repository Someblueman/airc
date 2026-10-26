package core

import (
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

func TestReactionReceiptEvidence(t *testing.T) {
	request := Request{Version: 1, RequestID: "reaction-1", Operation: ReactionAdd, Target: &Target{Kind: "room", Name: "#Room"}, ParentID: strings.Repeat("a", 32), Reaction: "checking"}
	if err := request.Normalize(); err != nil {
		t.Fatal(err)
	}
	receipt := &irc.SendReceiptEvent{
		Type:         "send_receipt",
		ChatMetadata: irc.ChatMetadata{},
		ID:           strings.Repeat("b", 32),
		From:         "author",
		Target:       "#Room",
		Message:      "checking",
		Reaction:     "checking",
		ReplyTo:      request.ParentID,
		ThreadID:     request.ParentID,
		Timestamp:    time.Unix(1000, 0),
		Receipt:      &irc.ReceiptInfo{Accepted: true},
	}
	data, err := ReceiptBytes(receipt, request, "unused", "author")
	if err != nil || len(data) == 0 {
		t.Fatalf("valid reaction receipt rejected: %v", err)
	}
	for name, mutate := range map[string]func(*irc.SendReceiptEvent){
		"native request id": func(r *irc.SendReceiptEvent) { r.RequestID = "upstream" },
		"wrong message":     func(r *irc.SendReceiptEvent) { r.Message = "agree" },
		"wrong reaction":    func(r *irc.SendReceiptEvent) { r.Reaction = "agree" },
		"wrong parent":      func(r *irc.SendReceiptEvent) { r.ReplyTo = strings.Repeat("c", 32) },
		"wrong author":      func(r *irc.SendReceiptEvent) { r.From = "other" },
	} {
		t.Run(name, func(t *testing.T) {
			copy := *receipt
			mutate(&copy)
			if _, err := ReceiptBytes(&copy, request, "unused", "author"); err == nil {
				t.Fatal("invalid reaction receipt accepted")
			}
		})
	}
}
