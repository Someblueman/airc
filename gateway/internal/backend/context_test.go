package backend

import (
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/auth"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestContextProofRejectsForeignRoom(t *testing.T) {
	parent := strings.Repeat("a", 32)
	request := core.Request{ParentID: parent, Target: &core.Target{Kind: "room", Name: "#allowed"}}
	context := irc.ConversationContext{
		ContextSummary: irc.ContextSummary{TriggerID: parent, RootID: parent},
		Messages:       []irc.MessageMetadata{{ID: parent, Target: "#foreign", From: "author", Message: "secret", Timestamp: time.Now()}},
	}
	if _, err := validateContextProof(request, context); err != auth.ErrForbidden {
		t.Fatalf("foreign room proof error = %v, want forbidden", err)
	}
}

func TestContextSnapshotPreservesRootGapButRequiresTrigger(t *testing.T) {
	parent, root := strings.Repeat("a", 32), strings.Repeat("b", 32)
	request := core.Request{ParentID: parent, Target: &core.Target{Kind: "room", Name: "#allowed"}}
	valid := irc.ConversationContext{
		ContextSummary: irc.ContextSummary{TriggerID: parent, RootID: root, Missing: []string{root}, OmittedMessages: 2},
		Messages:       []irc.MessageMetadata{{ID: parent, ThreadID: root, Target: "#allowed", From: "author", Message: "reply", Timestamp: time.Now()}},
	}
	if err := validateContextSnapshot(request, root, valid); err != nil {
		t.Fatalf("root gap rejected: %v", err)
	}
	valid.Missing = []string{parent}
	if err := validateContextSnapshot(request, root, valid); err != auth.ErrForbidden {
		t.Fatalf("missing trigger error = %v, want forbidden", err)
	}
}

func TestContextResultBudgetRejectsOversizedSnapshot(t *testing.T) {
	result := core.ContextResult{Summary: irc.ContextSummary{TriggerID: strings.Repeat("a", 32), RootID: strings.Repeat("a", 32)}}
	for i := 0; i < 32; i++ {
		result.Messages = append(result.Messages, irc.MessageMetadata{
			ID: strings.Repeat("a", 31) + string(rune('0'+i%10)), Target: "#allowed", From: "author", Message: strings.Repeat("x", 4096), Timestamp: time.Now(),
		})
	}
	if contextResultWithinBudget(result) {
		t.Fatal("oversized context result accepted")
	}
}

func TestContextSnapshotRejectsForeignThreadAndMalformedSummary(t *testing.T) {
	parent, root := strings.Repeat("a", 32), strings.Repeat("b", 32)
	request := core.Request{ParentID: parent, Target: &core.Target{Kind: "room", Name: "#allowed"}}
	base := irc.ConversationContext{
		ContextSummary: irc.ContextSummary{TriggerID: parent, RootID: root},
		Messages: []irc.MessageMetadata{{ID: parent, ThreadID: root, Target: "#allowed", From: "author", Message: "root", Timestamp: time.Now()},
			{ID: strings.Repeat("c", 32), ThreadID: strings.Repeat("d", 32), Target: "#allowed", From: "author", Message: "foreign thread", Timestamp: time.Now()}},
	}
	if err := validateContextSnapshot(request, root, base); err != auth.ErrForbidden {
		t.Fatalf("foreign thread error = %v, want forbidden", err)
	}
	base.Messages = base.Messages[:1]
	base.ProtectedIDs = []string{strings.Repeat("d", 32)}
	if err := validateContextSnapshot(request, root, base); err != auth.ErrForbidden {
		t.Fatalf("unreturned protected ID error = %v, want forbidden", err)
	}
	base.ProtectedIDs = nil
	base.OmittedMessages = -1
	if err := validateContextSnapshot(request, root, base); err != auth.ErrForbidden {
		t.Fatalf("negative omission error = %v, want forbidden", err)
	}
}

func TestContextSnapshotRejectsDuplicateProfiles(t *testing.T) {
	parent := strings.Repeat("a", 32)
	request := core.Request{ParentID: parent, Target: &core.Target{Kind: "room", Name: "#allowed"}}
	context := irc.ConversationContext{
		ContextSummary: irc.ContextSummary{TriggerID: parent, RootID: parent},
		Messages:       []irc.MessageMetadata{{ID: parent, Target: "#allowed", From: "author", Message: "root", Timestamp: time.Now()}},
		Participants:   []irc.AgentCard{{Nick: "Author", State: "unknown"}, {Nick: "author", State: "unknown"}},
	}
	if err := validateContextSnapshot(request, parent, context); err != auth.ErrForbidden {
		t.Fatalf("duplicate profile error = %v, want forbidden", err)
	}
}

func TestContextCollectorBoundaryValidation(t *testing.T) {
	parent := strings.Repeat("a", 32)
	base := irc.ConversationContext{ContextSummary: irc.ContextSummary{TriggerID: parent, RootID: parent}, Messages: []irc.MessageMetadata{{ID: parent, Target: "#allowed", From: "author", Message: "root", Timestamp: time.Now()}}}
	for name, mutate := range map[string]func(*irc.ConversationContext){
		"returned root marked missing": func(c *irc.ConversationContext) { c.Missing = []string{parent} },
		"too many protected IDs": func(c *irc.ConversationContext) {
			c.ProtectedIDs = []string{parent, strings.Repeat("b", 32), strings.Repeat("c", 32), strings.Repeat("d", 32), strings.Repeat("e", 32)}
		},
		"duplicate summary IDs": func(c *irc.ConversationContext) { c.Missing = []string{parent, parent} },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := base
			candidate.Missing = nil
			candidate.ProtectedIDs = nil
			mutate(&candidate)
			if name == "returned root marked missing" {
				if validateContextSnapshot(core.Request{ParentID: parent, Target: &core.Target{Kind: "room", Name: "#allowed"}}, parent, candidate) != auth.ErrForbidden {
					t.Fatal("returned root marked missing accepted")
				}
				return
			}
			if validContextSummary(candidate) {
				t.Fatal("malformed native summary accepted")
			}
		})
	}
	if chatEntryBare(irc.ChatEntry{Action: "context", Text: "mixed"}) {
		t.Fatal("mixed context payload accepted as bare entry")
	}
}
