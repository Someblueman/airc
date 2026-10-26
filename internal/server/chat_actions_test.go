package server

import (
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Someblueman/airc/internal/protocol"
)

func TestChatActionTableIsComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, action := range chatActionTable {
		if action.name == "" || action.handler == nil {
			t.Errorf("entry %+v lacks a name or a handler", action)
		}
		if seen[action.name] {
			t.Errorf("%s is listed twice", action.name)
		}
		seen[action.name] = true
		if action.requires != "" && !slices.Contains(chatFeatures, action.requires+"=1") {
			t.Errorf("%s requires %s, which the server does not advertise", action.name, action.requires)
		}
		if chatReadOnlyActions[action.name] != action.readOnly {
			t.Errorf("%s: readOnly=%v but chatReadOnlyActions says %v", action.name, action.readOnly, chatReadOnlyActions[action.name])
		}
	}
	if len(chatActions) != len(chatActionTable) {
		t.Errorf("%d actions by name, %d in the table", len(chatActions), len(chatActionTable))
	}
	for name := range chatReadOnlyActions {
		if !seen[name] {
			t.Errorf("read-only action %s is not in the table", name)
		}
	}
}

// chatResponse runs r as actor's CHAT request and returns the lines answered,
// the last of which is 778 when the request succeeded.
func chatResponse(t *testing.T, s *Server, actor *session, r protocol.ChatRequest) []string {
	t.Helper()
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	s.chatLocked(actor, protocol.Command{Name: "CHAT", Trailing: string(data)})
	var lines []string
	for len(actor.out) > 0 {
		lines = append(lines, <-actor.out)
	}
	if len(lines) == 0 {
		t.Fatalf("%s answered nothing", r.Action)
	}
	return lines
}

// TestChatReadOnlyActionsAppendNothing drives every action the table marks
// read-only to a successful answer and checks that history is unchanged, since
// serializesPosts lets those actions run without the post lock.
func TestChatReadOnlyActionsAppendNothing(t *testing.T) {
	s := New(Config{HistoryLimit: 64})
	if err := s.RestoreChat(filepath.Join(t.TempDir(), "chat")); err != nil {
		t.Fatal(err)
	}
	actor := &session{server: s, client: Client{Nick: "reader"}, ephemeral: true, out: make(chan string, 1024), done: make(chan struct{})}
	question := Message{ID: newID(), From: "author", Target: "#room", Body: "which?"}
	s.history.add(question)
	lines := chatResponse(t, s, actor, protocol.ChatRequest{Action: "poll", Target: "#room", Text: "Which?", Options: []string{"a", "b"}, Seconds: 60})
	var pollID string
	for _, line := range lines {
		if command, _ := protocol.Parse(line); command.Name == "777" {
			var entry protocol.ChatEntry
			if err := json.Unmarshal([]byte(command.Trailing), &entry); err != nil {
				t.Fatal(err)
			}
			pollID = entry.ID
		}
	}
	if pollID == "" || !containsNumeric(lines[len(lines)-1], "778") {
		t.Fatalf("poll answered %q", lines)
	}
	requests := map[string]protocol.ChatRequest{
		"context":   {ID: question.ID, Limit: 10},
		"pins":      {Target: "#room"},
		"waiting":   {ID: question.ID},
		"prepare":   {ID: question.ID, Seconds: 60},
		"cancel":    {ID: question.ID},
		"typing":    {Target: "#room", Seconds: 5},
		"thinking":  {Target: "#room", Seconds: 5},
		"results":   {ID: pollID},
		"handoffs":  {},
		"decisions": {},
		// The paste actions run in table order, so one upload goes through.
		"paste-begin": {Token: "upload", Name: "note.txt", Size: 5, Lines: 1},
		"paste-chunk": {Token: "upload", Seq: 0, Data: base64.StdEncoding.EncodeToString([]byte("hello"))},
		"paste-end":   {Token: "upload"},
		"paste-get":   {ID: protocol.PasteID([]byte("hello"))},
		"pastes":      {},
	}
	size, bytes := s.history.size, s.history.bytes
	for _, action := range chatActionTable {
		if !action.readOnly {
			continue
		}
		r, ok := requests[action.name]
		if !ok {
			t.Fatalf("no request drives read-only action %s", action.name)
		}
		r.Action = action.name
		lines := chatResponse(t, s, actor, r)
		if last := lines[len(lines)-1]; !containsNumeric(last, "778") {
			t.Errorf("%s was rejected: %q", action.name, last)
		}
		if s.history.size != size || s.history.bytes != bytes {
			t.Errorf("%s changed history from %d messages to %d", action.name, size, s.history.size)
		}
	}
}
