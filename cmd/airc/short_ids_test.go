package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/pkg/irc"
)

// shortIDServer starts a daemon with chat state and pastes whose history
// already holds two messages sharing their first eight characters, so that
// prefix is ambiguous and nine characters tell them apart.
func shortIDServer(t *testing.T) (address, twinA, twinB string) {
	t.Helper()
	agentEnv(t)
	dir := t.TempDir()
	twinA, twinB = "0a0a0a0a"+strings.Repeat("b", 24), "0a0a0a0a"+strings.Repeat("c", 24)
	seed := `{"id":"` + twinA + `","from":"planner","target":"#work","message":"twin a"}` + "\n" +
		`{"id":"` + twinB + `","from":"planner","target":"#work","message":"twin b"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "history"), []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	address = cliTestServerSetup(t, server.Config{HistoryLimit: 64}, func(s *server.Server) error {
		for _, err := range []error{s.RestoreHistory(filepath.Join(dir, "history")), s.RestoreChat(filepath.Join(dir, "chat")), s.RestorePastes(filepath.Join(dir, "pastes.json"))} {
			if err != nil {
				return err
			}
		}
		return nil
	})
	return address, twinA, twinB
}

// failedCLI runs a --json command that must fail and returns its error object.
func failedCLI(t *testing.T, address string, args ...string) commandFailure {
	t.Helper()
	_, stderr, err := cli(t, address, append(args, "--json")...)
	if err == nil {
		t.Fatalf("airc %s succeeded", strings.Join(args, " "))
	}
	var failure commandFailure
	if err := json.Unmarshal([]byte(stderr), &failure); err != nil {
		t.Fatalf("airc %s wrote %q, not an error object: %v", strings.Join(args, " "), stderr, err)
	}
	return failure
}

// Every command that takes an ID accepts its first eight or more characters;
// what it prints and keeps locally names the full ID the daemon answered.
func TestAbbreviatedIDsThroughTheCLI(t *testing.T) {
	address, twinA, _ := shortIDServer(t)
	question := send(t, address, "planner", "work", "builder: which branch?")
	short := question.ID[:8]

	thread := jsonLines[checkMessage](t, mustCLI(t, address, "thread", short, "--json"))
	if len(thread) != 1 || thread[0].ID != question.ID {
		t.Fatalf("thread by prefix %+v", thread)
	}
	reply := cliJSON[irc.SendReceiptEvent](t, address, "send", "--nick", "builder", "--reply-to", short, "--message", "work-7")
	if reply.ReplyTo != question.ID || reply.ThreadID != question.ID || reply.Target != "#work" {
		t.Fatalf("reply by prefix %+v", reply)
	}
	if reaction := cliJSON[irc.SendReceiptEvent](t, address, "react", question.ID[:12], "seen", "--nick", "builder"); reaction.ReplyTo != question.ID || reaction.Reaction != "seen" {
		t.Fatalf("reaction by prefix %+v", reaction)
	}
	if context := cliJSON[conversationContext](t, address, "context", short); context.TriggerID != question.ID || context.RootID != question.ID || len(context.Messages) != 3 {
		t.Fatalf("context by prefix %+v", context)
	}

	offer := cliJSON[handoffRow](t, address, "handoff", "--nick", "planner", "--channel", "work", "--to", "builder", "--message", "finish task 7")
	if accepted := cliJSON[handoffRow](t, address, "handoff", "accept", offer.ID[:8], "--nick", "builder"); accepted.ID != offer.ID || accepted.State != "accepted" || accepted.Holder != "builder" {
		t.Fatalf("accept by prefix %+v", accepted)
	}
	if listed := jsonLines[handoffRow](t, mustCLI(t, address, "handoffs", "--id", offer.ID[:10], "--json")); len(listed) != 1 || listed[0].ID != offer.ID {
		t.Fatalf("handoffs --id by prefix %+v", listed)
	}
	ask := cliJSON[decisionRow](t, address, "decide", "--nick", "planner", "--to", "builder", "--message", "ship it?")
	if answered := cliJSON[decisionRow](t, address, "decide", "answer", ask.ID[:9], "--nick", "builder", "--message", "yes"); answered.ID != ask.ID || answered.State != "answered" {
		t.Fatalf("answer by prefix %+v", answered)
	}

	path, content := testPasteFile(t, "run.log", 3000)
	put := cliJSON[pasteRow](t, address, "paste", "put", path, "--nick", "planner")
	if got := cliJSON[pasteRow](t, address, "paste", "get", put.ID[:8]); got.ID != put.ID || got.Content != string(content) {
		t.Fatalf("paste get by prefix %+v", got)
	}
	if got := mustCLI(t, address, "paste", "get", put.ID[:16]); got != string(content) {
		t.Fatalf("paste get by prefix returned %d bytes, want %d", len(got), len(content))
	}
	if info := cliJSON[pasteRow](t, address, "paste", "info", put.ID[:8]); info.ID != put.ID || info.Size != len(content) {
		t.Fatalf("paste info by prefix %+v", info)
	}

	// A follow is kept by the full root, whichever abbreviation named it, and
	// an abbreviation removes it.
	if out := mustCLI(t, address, "follow", reply.ID[:8], "--nick", "builder"); out != question.ID+"\n" {
		t.Fatalf("follow by prefix printed %q", out)
	}
	if out := mustCLI(t, address, "unfollow", question.ID[:10], "--nick", "builder"); out != "" {
		t.Fatalf("unfollow by prefix left %q", out)
	}
	if out := mustCLI(t, address, "resolve", short, "--nick", "builder"); out != "resolved "+question.ID+" in #work by builder: Resolved\n" {
		t.Fatalf("resolve by prefix printed %q", out)
	}

	// The seeded twins make their eight characters ambiguous: a bad argument,
	// not retryable; nine resolve it.
	for _, args := range [][]string{
		{"thread", "0a0a0a0a"},
		{"context", "0a0a0a0a"},
		{"send", "--nick", "builder", "--reply-to", "0a0a0a0a", "--message", "which?"},
		{"react", "0a0a0a0a", "seen", "--nick", "builder"},
		{"pin", "0a0a0a0a", "--nick", "builder"},
		{"handoff", "accept", "0a0a0a0a", "--nick", "builder"},
		{"follow", "0a0a0a0a", "--nick", "builder"},
	} {
		failure := failedCLI(t, address, args...)
		if failure.Code != "server_rejected" || failure.Retryable || !strings.Contains(failure.Message, "ambiguous ID prefix 0a0a0a0a; give more of it") {
			t.Errorf("airc %s: %+v", strings.Join(args, " "), failure)
		}
	}
	if thread := jsonLines[checkMessage](t, mustCLI(t, address, "thread", "0a0a0a0ab", "--json")); len(thread) != 1 || thread[0].ID != twinA {
		t.Fatalf("nine characters resolve the twin: %+v", thread)
	}
	// An abbreviation nothing matches is an unknown ID, worded as before;
	// seven characters are not an abbreviation at all.
	if failure := failedCLI(t, address, "thread", "fedcba9876"); failure.Code != "invalid_target" || !strings.Contains(failure.Message, "no longer retained") {
		t.Fatalf("an unknown thread: %+v", failure)
	}
	if failure := failedCLI(t, address, "paste", "get", "fedcba9876"); !strings.Contains(failure.Message, "paste is not retained") {
		t.Fatalf("an unknown paste: %+v", failure)
	}
	if failure := failedCLI(t, address, "thread", question.ID[:7]); failure.Code != "invalid_request" || !strings.Contains(failure.Message, "usage: airc thread") {
		t.Fatalf("seven characters: %+v", failure)
	}
}
