package backend

import (
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestSelectorTargetsAndMembership(t *testing.T) {
	root := strings.Repeat("a", 32)
	child := strings.Repeat("b", 32)
	room := &core.Target{Kind: "room", Name: "#room"}
	thread := core.Request{Target: room, ThreadMode: "thread", ParentID: root}
	replies := core.Request{Target: room, ThreadMode: "replies", ParentID: root}
	if got := nativeSelector(thread); got != "thread:"+root {
		t.Fatalf("thread selector = %q", got)
	}
	if got := nativeSelector(replies); got != "replies:"+root {
		t.Fatalf("replies selector = %q", got)
	}
	rootMessage := &irc.MessageMetadata{ID: root, Target: "#room", From: "author", Message: "root", Timestamp: time.Now()}
	childMessage := &irc.MessageMetadata{ID: child, ThreadID: root, Target: "#room", From: "author", Message: "child", Timestamp: time.Now()}
	replyMessage := &irc.MessageMetadata{ID: child, ReplyTo: root, ThreadID: root, Target: "#room", From: "author", Message: "reply", Timestamp: time.Now()}
	foreign := &irc.MessageMetadata{ID: child, ThreadID: root, Target: "#other", From: "author", Message: "foreign", Timestamp: time.Now()}
	reaction := &irc.MessageMetadata{ID: child, ReplyTo: root, Reaction: "agree", Target: "#room", From: "author", Message: "agree", Timestamp: time.Now()}
	if !selectorMessageAllowed(rootMessage, thread) || !selectorMessageAllowed(childMessage, thread) || selectorMessageAllowed(foreign, thread) {
		t.Fatal("thread membership validation incorrect")
	}
	if selectorMessageAllowed(rootMessage, replies) || selectorMessageAllowed(reaction, replies) || !selectorMessageAllowed(replyMessage, replies) {
		t.Fatal("reply membership validation incorrect")
	}
}

func TestHistoryPageValidatesSelectorMarkerAndMessages(t *testing.T) {
	root := strings.Repeat("a", 32)
	child := strings.Repeat("b", 32)
	r := core.Request{Target: &core.Target{Kind: "room", Name: "#room"}, ThreadMode: "thread", ParentID: root, Limit: 2}
	message := &irc.MessageMetadata{ID: child, ThreadID: root, Target: "#room", From: "author", Message: "child", Timestamp: time.Now()}
	entries := []irc.CheckEntry{{Kind: "message", Target: "#room", Targets: []int{0}, Message: message}, {Kind: "page", Target: "thread:" + root, Status: "ok", Cursor: child}}
	if page, err := historyPage(entries, r); err != nil || len(page.Messages) != 1 || page.Cursor != child {
		t.Fatalf("valid selector page = %+v, err %v", page, err)
	}
	entries[1].Target = "#room"
	if _, err := historyPage(entries, r); err == nil {
		t.Fatal("ordinary marker accepted for thread selector")
	}
}

func TestThreadSelectorAcceptsRetainedDescendantAfterRootGap(t *testing.T) {
	root := strings.Repeat("a", 32)
	child := strings.Repeat("b", 32)
	r := core.Request{Target: &core.Target{Kind: "room", Name: "#room"}, ThreadMode: "thread", ParentID: root}
	proof := irc.ConversationContext{
		ContextSummary: irc.ContextSummary{TriggerID: root, RootID: root, Missing: []string{root}},
		Messages:       []irc.MessageMetadata{{ID: child, ThreadID: root, Target: "#room", From: "author", Message: "retained child", Timestamp: time.Now()}},
	}
	if got, ok := validateThreadSelectorProof(r, proof); !ok || got != root {
		t.Fatalf("root-gap proof = %q, %v", got, ok)
	}
	proof.Messages[0].Target = "#foreign"
	if _, ok := validateThreadSelectorProof(r, proof); ok {
		t.Fatal("foreign-room root-gap proof accepted")
	}
}

func TestRepliesSelectorAcceptsRetainedDirectChildAfterParentGap(t *testing.T) {
	parent := strings.Repeat("a", 32)
	child := strings.Repeat("b", 32)
	r := core.Request{Target: &core.Target{Kind: "room", Name: "#room"}, ThreadMode: "replies", ParentID: parent, Limit: 1}
	entries := []irc.CheckEntry{
		{Kind: "message", Target: "#room", Targets: []int{0}, Message: &irc.MessageMetadata{ID: child, ReplyTo: parent, ThreadID: parent, Target: "#room", From: "author", Message: "direct child", Timestamp: time.Now()}},
		{Kind: "page", Target: "replies:" + parent, Status: "ok", Cursor: child},
	}
	if page, err := historyPage(entries, r); err != nil || len(page.Messages) != 1 || page.Messages[0].ID != child {
		t.Fatalf("direct-child gap page = %+v, err %v", page, err)
	}
}
