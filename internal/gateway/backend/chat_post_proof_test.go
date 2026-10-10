package backend

import (
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestDirectCorrectionProofUsesStableAuthorAndExactDestination(t *testing.T) {
	id, account := strings.Repeat("a", 32), strings.Repeat("b", 32)
	request := core.Request{Operation: core.MessageCorrect, Target: &core.Target{Kind: "direct", Name: "peer"}, ParentID: id}
	card := irc.AgentCard{Nick: "current-author", AccountID: account}
	proof := irc.ConversationContext{ContextSummary: irc.ContextSummary{TriggerID: id, RootID: id}, Messages: []irc.MessageMetadata{{ChatMetadata: irc.ChatMetadata{AccountID: account}, ID: id, From: "previous-author", Target: "PEER", Message: "original", Timestamp: time.Now().UTC()}}}
	if !validDirectCorrectionProof(request, proof, card) {
		t.Fatal("valid stable author proof rejected")
	}
	for name, mutate := range map[string]func(*irc.ConversationContext, *irc.AgentCard){
		"replacement-account": func(p *irc.ConversationContext, c *irc.AgentCard) { c.AccountID = strings.Repeat("c", 32) },
		"missing-account":     func(p *irc.ConversationContext, c *irc.AgentCard) { c.AccountID = "" },
		"foreign-target":      func(p *irc.ConversationContext, c *irc.AgentCard) { p.Messages[0].Target = "outsider" },
		"foreign-id":          func(p *irc.ConversationContext, c *irc.AgentCard) { p.Messages[0].ID = strings.Repeat("d", 32) },
		"root-mismatch":       func(p *irc.ConversationContext, c *irc.AgentCard) { p.RootID = strings.Repeat("e", 32) },
	} {
		t.Run(name, func(t *testing.T) {
			copy := proof
			copy.Messages = append([]irc.MessageMetadata(nil), proof.Messages...)
			copyCard := card
			mutate(&copy, &copyCard)
			if validDirectCorrectionProof(request, copy, copyCard) {
				t.Fatal("foreign direct proof accepted")
			}
		})
	}
}
