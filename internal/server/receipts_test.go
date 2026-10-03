package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/protocol"
)

func receiptFromLine(t *testing.T, line string) protocol.MessageMetadata {
	t.Helper()
	c, err := protocol.Parse(line)
	if err != nil || c.Name != "762" {
		t.Fatal(line, err)
	}
	m, err := protocol.DecodeMessageMetadata(c.Trailing)
	if err != nil || m.Receipt == nil {
		t.Fatal(line, err)
	}
	return m
}

func TestReceiptsReportSyncedHistoryAndRecoverAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history")
	s := New(Config{HistoryLimit: 2})
	if err := s.RestoreHistory(path); err != nil {
		t.Fatal(err)
	}
	m := s.newMessage("writer", "#room", "once", nil)
	m.RequestID = "stable"
	s.mu.Lock()
	s.recordLocked(&m)
	s.mu.Unlock()
	if !m.Persisted {
		t.Fatal("write was not synced")
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	restored := New(Config{HistoryLimit: 2})
	if err := restored.RestoreHistory(path); err != nil {
		t.Fatal(err)
	}
	defer restored.Shutdown(context.Background())
	actor := &session{client: Client{Nick: "writer"}, out: make(chan string, 8), done: make(chan struct{})}
	restored.retryRequestLocked(actor, protocol.Command{Params: []string{"stable"}})
	receipt := receiptFromLine(t, <-actor.out)
	if receipt.ID != m.ID || !receipt.Receipt.Accepted || !receipt.Receipt.Persisted {
		t.Fatal(receipt)
	}
	actor.client.Nick = "other"
	restored.retryRequestLocked(actor, protocol.Command{Params: []string{"stable"}})
	if !containsNumeric(<-actor.out, "488") {
		t.Fatal("another nickname recovered the request")
	}
}

func TestHistoryWriteFailureIsAcceptedButNotPersisted(t *testing.T) {
	s := New(Config{HistoryLimit: 2})
	f, err := os.CreateTemp(t.TempDir(), "closed-history")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	s.histFile = f
	m := s.newMessage("writer", "offline", "still available", nil)
	s.mu.Lock()
	s.recordLocked(&m)
	s.mu.Unlock()
	actor := &session{client: Client{Nick: "writer"}, out: make(chan string, 8), done: make(chan struct{})}
	s.receiptLocked(actor, m, true)
	r := receiptFromLine(t, <-actor.out)
	if !r.Receipt.Accepted || r.Receipt.Persisted || r.Receipt.RecipientConnected == nil || *r.Receipt.RecipientConnected || s.persistenceError == "" {
		t.Fatal(r)
	}
	if stored, ok := s.history.message(m.ID); !ok || stored.Persisted {
		t.Fatal("memory message was lost or marked persisted")
	}
}
