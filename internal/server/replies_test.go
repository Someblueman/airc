package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplyLinksSurviveHistoryRestoreAndCompaction(t *testing.T) {
	root := strings.Repeat("a", 32)
	first := strings.Repeat("b", 32)
	last := strings.Repeat("c", 32)
	messages := []Message{
		{ID: root, From: "alice", Target: "#room", Body: "question"},
		{ID: first, ReplyTo: root, ThreadID: root, From: "bob", Target: "#room", Body: "answer"},
		{ID: last, ReplyTo: first, ThreadID: root, From: "alice", Target: "#room", Body: "follow-up"},
	}
	var data []byte
	for _, message := range messages {
		line, _ := json.Marshal(message)
		data = append(data, append(line, '\n')...)
	}
	path := filepath.Join(t.TempDir(), "history.jsonl")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	for run := 0; run < 2; run++ {
		srv := New(Config{HistoryLimit: 2})
		if err := srv.RestoreHistory(path); err != nil {
			t.Fatal(err)
		}
		selected, err := srv.history.conversation("thread:" + last)
		if err != nil || selected != "thread:"+root {
			t.Fatalf("root resolution after restore: %s %v", selected, err)
		}
		got, status := srv.history.since(selected, "*", 10)
		if status != historyOK || len(got) != 2 || got[0].ReplyTo != root || got[1].ReplyTo != first {
			t.Fatalf("restored links: %+v %s", got, status)
		}
		if _, found := srv.history.message(root); found {
			t.Fatal("root should have been compacted away")
		}
		if err := srv.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}
