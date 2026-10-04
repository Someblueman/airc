package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Scorecard workflow v1: a deterministic adapter test, not an LLM reasoning
// evaluation. Fixture setup and independent verification are outside the three
// participant calls; there are no hidden discovery, retry or polling calls.
func TestConversationEffortThreeMCPCalls(t *testing.T) {
	address := chatServer(t, 64)
	question := posted(t, address, "planner", "#room", "What timeout should we use? Include the pinned deployment constraint.")
	obsolete := replyMessage(t, address, "reviewer", question.ID, "Use 10 seconds.")
	var correction irc.ChatEntry
	if err := json.Unmarshal([]byte(mustCLI(t, address, "correct", obsolete.ID, "--nick", "reviewer", "--message", "Use 30 seconds.", "--json")), &correction); err != nil {
		t.Fatal(err)
	}
	pin := posted(t, address, "planner", "#room", "Deploy only after the maintenance window.")
	mustCLI(t, address, "pin", pin.ID, "--nick", "planner")
	for i := range 10 {
		posted(t, address, "other", "#room", fmt.Sprintf("Unrelated conversation %d", i))
	}
	mustCLI(t, address, "user", "create", "--nick", "participant")
	binary := filepath.Join(t.TempDir(), "airc")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %s %v", out, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "scorecard", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: exec.Command(binary, "mcp", "--addr", address, "--nick", "participant")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	calls, returnedBytes := 0, 0
	call := func(name string, args any) []json.RawMessage {
		t.Helper()
		calls++
		r, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || r.IsError {
			t.Fatalf("call %d %s: %+v %v", calls, name, r, err)
		}
		wire, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		returnedBytes += len(wire)
		data, _ := json.Marshal(r.StructuredContent)
		var output struct {
			Rows []json.RawMessage `json:"rows"`
		}
		if err := json.Unmarshal(data, &output); err != nil {
			t.Fatal(err)
		}
		return output.Rows
	}
	rows := call("context", map[string]any{"id": question.ID})
	var conversation conversationContext
	if len(rows) != 1 || json.Unmarshal(rows[0], &conversation) != nil {
		t.Fatal("missing context", rows)
	}
	if len(conversation.Pins) != 1 || conversation.Pins[0].ID != pin.ID || len(conversation.Messages) != 3 {
		t.Fatal("pin or conversation missing", conversation)
	}
	var current string
	for _, message := range conversation.Messages {
		if message.ID == obsolete.ID && message.SupersededBy != correction.Message.ID {
			t.Fatal("correction link missing")
		}
		if message.Kind == "correct" && message.Supersedes == obsolete.ID {
			current = message.Message
		}
	}
	if current != "Use 30 seconds." || conversation.Pins[0].Message != pin.Message {
		t.Fatal("current answer or pin text missing")
	}
	answer := current + " " + conversation.Pins[0].Message
	rows = call("send", map[string]any{"reply_to": question.ID, "message": answer})
	var sent irc.MessageEvent
	if len(rows) != 1 || json.Unmarshal(rows[0], &sent) != nil || sent.ReplyTo != question.ID || sent.Message != answer || sent.AccountID == "" {
		t.Fatal("incorrect or unauthenticated reply", rows)
	}
	// The scripted peer responds after the participant's accepted reply. CHECK
	// must observe it in one invocation, whether retained or arriving during wait.
	peer := replyMessage(t, address, "reviewer", sent.ID, "Confirmed: use the corrected timeout and pinned constraint.")
	rows = call("check", map[string]any{"reply_to": sent.ID, "wait_seconds": 2})
	observed := false
	for _, row := range rows {
		var message irc.MessageEvent
		if json.Unmarshal(row, &message) == nil && message.ID == peer.ID && message.ReplyTo == sent.ID && message.Message == peer.Message {
			observed = true
		}
	}
	if !observed || calls != 3 {
		t.Fatalf("observed=%v calls=%d", observed, calls)
	}
	// Independent history audit: exactly one participant post was accepted.
	posts := 0
	for line := range strings.SplitSeq(strings.TrimSpace(mustCLI(t, address, "history", "#room", "--limit", "64", "--json")), "\n") {
		var message irc.MessageEvent
		if json.Unmarshal([]byte(line), &message) != nil {
			t.Fatal("invalid history", line)
		}
		if message.From == "participant" {
			posts++
		}
	}
	if posts != 1 {
		t.Fatalf("accepted %d participant posts", posts)
	}
	t.Logf("workflow=conversation-effort-v1 calls=%d returned_result_bytes=%d accepted_posts=%d peer_observed=%v", calls, returnedBytes, posts, observed)
}
