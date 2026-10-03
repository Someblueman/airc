package main

import (
	"encoding/json"
	"github.com/Someblueman/airc/pkg/irc"
	"strings"
	"testing"
)

func TestContextPreservesOriginalCorrectionsPinsProfilesAndOmissions(t *testing.T) {
	address := chatServer(t, 64)
	root := posted(t, address, "alice", "#room", "original question")
	mustCLI(t, address, "profile", "--nick", "alice", "--model", "self-reported-model")
	mustCLI(t, address, "pin", root.ID, "--nick", "alice")
	reply := replyMessage(t, address, "bob", root.ID, "original answer")
	correction := mustCLI(t, address, "correct", reply.ID, "--nick", "bob", "--message", "corrected answer", "--json")
	var entry irc.ChatEntry
	if err := json.Unmarshal([]byte(correction), &entry); err != nil || entry.Message == nil || entry.Message.Message != "corrected answer" {
		t.Fatal(correction, err)
	}
	for i := 0; i < 6; i++ {
		replyMessage(t, address, "bob", root.ID, strings.Repeat("x", 250))
	}
	var r conversationContext
	out := mustCLI(t, address, "context", reply.ID, "--limit", "3", "--json")
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatal(err)
	}
	if r.TriggerID != reply.ID || r.RootID != root.ID || r.OmittedMessages != 6 || len(r.Messages) != 3 || len(r.Pins) != 1 || len(r.Participants) != 2 {
		t.Fatalf("incomplete context: %s", out)
	}
	if r.Messages[1].Message != "original answer" || r.Messages[1].SupersededBy == "" || r.Messages[2].Message != "corrected answer" {
		t.Fatal(out)
	}
	human := mustCLI(t, address, "context", reply.ID, "--limit", "3")
	if !strings.Contains(human, "[superseded;") || !strings.Contains(human, "[correction of ") || !strings.Contains(human, "pin ") {
		t.Fatal(human)
	}
	if !strings.Contains(out, "self-reported-model") {
		t.Fatal("profile missing", out)
	}
	out = mustCLI(t, address, "context", root.ID, "--max-bytes", "1200", "--json")
	if len(out) > 1200 {
		t.Fatal("byte budget ignored")
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatal(err)
	}
	if r.OmittedMessages == 0 || len(r.Messages) == 0 || r.Messages[0].ID != root.ID {
		t.Fatal(out)
	}
}

func TestContextReportsEvictedRoot(t *testing.T) {
	address := chatServer(t, 2)
	root := posted(t, address, "alice", "#room", "question")
	reply := replyMessage(t, address, "bob", root.ID, "answer")
	replyMessage(t, address, "bob", reply.ID, "nested")
	var r conversationContext
	out := mustCLI(t, address, "context", reply.ID, "--json")
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatal(err)
	}
	if r.RootID != root.ID || len(r.Missing) != 1 || r.Missing[0] != root.ID || len(r.Messages) != 2 {
		t.Fatal(out)
	}
}
