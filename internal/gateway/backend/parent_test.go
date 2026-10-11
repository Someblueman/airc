package backend

import (
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestParentSnapshotMustProveExactParentAndRoot(t *testing.T) {
	parentID, rootID := strings.Repeat("a", 32), strings.Repeat("b", 32)
	a := store.Attempt{Job: store.Job{Author: core.Author{Nick: "author"}, Request: core.Request{Operation: core.Reply, ParentID: parentID, Target: &core.Target{Kind: "room", Name: "#Allowed"}}}}
	valid := func() irc.ConversationContext {
		return irc.ConversationContext{ContextSummary: irc.ContextSummary{TriggerID: parentID, RootID: rootID}, Messages: []irc.MessageMetadata{{ID: parentID, From: "peer", Target: "#Allowed", ThreadID: rootID}}}
	}
	if root, ok := parentConversation(a, valid()); !ok || root != rootID {
		t.Fatal("valid parent denied")
	}
	mutations := []func(*irc.ConversationContext){
		func(c *irc.ConversationContext) { c.TriggerID = rootID },
		func(c *irc.ConversationContext) { c.Messages = nil },
		func(c *irc.ConversationContext) { c.Messages[0].ID = rootID },
		func(c *irc.ConversationContext) { c.Missing = []string{parentID} },
		func(c *irc.ConversationContext) { c.RootID = strings.Repeat("c", 32) },
		func(c *irc.ConversationContext) { c.Messages[0].Target = "#allowed" },
	}
	for _, mutate := range mutations {
		c := valid()
		mutate(&c)
		if _, ok := parentConversation(a, c); ok {
			t.Fatal("unproven parent/root accepted")
		}
	}
}
