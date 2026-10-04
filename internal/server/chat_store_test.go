package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Someblueman/airc/internal/protocol"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestQuotaRestorePreservesQuietRoomsAndRetryIndexes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history")
	s := New(Config{HistoryLimit: 6})
	if err := s.RestoreChat(filepath.Join(dir, "chat")); err != nil {
		t.Fatal(err)
	}
	next := s.copyChat()
	next.Rooms["#noisy"] = roomSettings{HistoryLimit: 3}
	if err := s.saveChatLocked(next); err != nil {
		t.Fatal(err)
	}
	quiet := Message{ID: newID(), From: "quiet", Target: "#quiet", Body: "preserve", Timestamp: time.Now()}
	all := []Message{quiet}
	for i := range 30 {
		all = append(all, Message{RequestID: fmt.Sprint(i), ID: newID(), From: "loud", Target: "#noisy", Body: "@reader traffic", Timestamp: time.Now()})
	}
	var data []byte
	for _, m := range all {
		line, _ := json.Marshal(m)
		data = append(data, append(line, '\n')...)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		restored := New(Config{HistoryLimit: 6})
		if err := restored.RestoreChat(filepath.Join(dir, "chat")); err != nil {
			t.Fatal(err)
		}
		if err := restored.RestoreHistory(path); err != nil {
			t.Fatal(err)
		}
		h := &restored.history
		if m, found := h.message(quiet.ID); !found || m.Body != "preserve" {
			t.Fatal("quiet history lost")
		}
		if h.size != 4 || len(h.positions) != 4 || len(h.requests) != 3 || len(h.recent("@reader", 100)) != 3 {
			t.Fatalf("indexes/quotas: size=%d positions=%d retries=%d", h.size, len(h.positions), len(h.requests))
		}
		for key, index := range h.requests {
			if requestKey(h.items[index].From, h.items[index].AccountID, h.items[index].RequestID) != key {
				t.Fatal("stale retry index")
			}
		}
		if err := restored.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAccountAndChatWritesFailBeforeApplying(t *testing.T) {
	s := New(Config{HistoryLimit: 8})
	dir := t.TempDir()
	if err := s.RestoreAccounts(filepath.Join(dir, "accounts")); err != nil {
		t.Fatal(err)
	}
	if err := s.RestoreChat(filepath.Join(dir, "chat")); err != nil {
		t.Fatal(err)
	}
	actor := &session{client: Client{Nick: "author"}, ephemeral: true, out: make(chan string, 8), done: make(chan struct{})}
	s.accountsAt = filepath.Join(dir, "missing", "accounts")
	s.authLocked(actor, protocol.Command{Name: "REGISTER", Params: []string{"author"}, Trailing: strings.Repeat("b", 64)})
	if len(s.accounts) != 0 || actor.accountID != "" || !containsNumeric(<-actor.out, "437") {
		t.Fatal("failed account write applied")
	}
	s.chatAt = filepath.Join(dir, "missing", "chat")
	m := Message{ID: newID(), From: "author", Target: "#room", Body: "original"}
	s.history.add(m)
	request, _ := json.Marshal(protocol.ChatRequest{Action: "pin", ID: m.ID})
	s.chatLocked(actor, protocol.Command{Trailing: string(request)})
	if len(s.chat.Pins) != 0 || !containsNumeric(<-actor.out, "461") {
		t.Fatal("failed pin write applied")
	}
	request, _ = json.Marshal(protocol.ChatRequest{Action: "correct", ID: m.ID, Text: "replacement"})
	s.chatLocked(actor, protocol.Command{Trailing: string(request)})
	old, _ := s.history.message(m.ID)
	if s.history.size != 1 || old.SupersededBy != "" || !containsNumeric(<-actor.out, "461") {
		t.Fatal("failed correction applied")
	}
}

func TestNewStoresRejectCorruptAndOversizedState(t *testing.T) {
	for _, data := range []string{"null", "{} {}", `{"extra":1}`, strings.Repeat("x", (8<<20)+1)} {
		dir := t.TempDir()
		path := filepath.Join(dir, "state")
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		s := New(Config{HistoryLimit: 8})
		if err := s.RestoreChat(path); err == nil {
			t.Fatal("corrupt chat accepted")
		}
		if err := s.RestoreAccounts(path); err == nil {
			t.Fatal("corrupt accounts accepted")
		}
	}
}
