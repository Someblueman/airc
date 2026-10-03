package server

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHistoryCompactsDuringRuntimeAndRestoresReceipts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history")
	s := New(Config{HistoryLimit: 3})
	if err := s.RestoreHistory(path); err != nil {
		t.Fatal(err)
	}
	var last Message
	for i := 0; i < 40; i++ {
		s.messageMu.Lock()
		s.mu.Lock()
		last = s.newMessage("writer", "#room", fmt.Sprint(i), nil)
		last.RequestID = fmt.Sprint("request-", i)
		s.recordLocked(&last)
		records := s.histRecords
		s.mu.Unlock()
		s.messageMu.Unlock()
		if !last.Persisted || records >= 6 {
			t.Fatalf("append not persisted/bounded: %+v, %d", last, records)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(string(data), "\n"); lines > 5 || lines < 3 {
		t.Fatalf("archive grew beyond retention slack: %d", lines)
	}
	s.Shutdown(context.Background())
	restored := New(Config{HistoryLimit: 3})
	if err := restored.RestoreHistory(path); err != nil {
		t.Fatal(err)
	}
	defer restored.Shutdown(context.Background())
	got, found := restored.history.message(last.ID)
	if !found || got.RequestID != last.RequestID || !got.Persisted || restored.seq != last.Seq {
		t.Fatalf("restore lost receipt: %+v", got)
	}
	if restored.history.size != 3 {
		t.Fatal(restored.history.size)
	}
}

func TestOutboundBytesAndCountOverloadReportError(t *testing.T) {
	for _, tc := range []struct {
		name            string
		bytes, capacity int
	}{{"bytes", 8, 8}, {"count", 1000, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			serverConn, peer := net.Pipe()
			defer peer.Close()
			s := New(Config{OutboundBytes: tc.bytes, PingInterval: time.Hour, ReadTimeout: time.Hour})
			c := &session{server: s, conn: serverConn, out: make(chan string, tc.capacity), done: make(chan struct{}), overload: make(chan struct{}, 1)}
			if !c.enqueue("12345") || c.enqueue("67890") {
				t.Fatal("queue limit not enforced")
			}
			if c.outBytes.Load() != 5 {
				t.Fatal(c.outBytes.Load())
			}
			done := make(chan struct{})
			go func() { c.writeLoop(); close(done) }()
			peer.SetReadDeadline(time.Now().Add(time.Second))
			line, err := bufio.NewReader(peer).ReadString('\n')
			if err != nil || !strings.Contains(line, "ERROR :outbound overload") {
				t.Fatalf("missing overload reason: %q %v", line, err)
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("writer leaked")
			}
			if c.enqueue("later") {
				t.Fatal("overloaded session accepted another message")
			}
		})
	}
}

func TestCompactionFailureStopsPersistenceWithoutLosingAcceptance(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "archive")
	path := filepath.Join(dir, "history")
	s := New(Config{HistoryLimit: 1})
	if err := s.RestoreHistory(path); err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	write := func(text string) Message {
		s.messageMu.Lock()
		defer s.messageMu.Unlock()
		s.mu.Lock()
		defer s.mu.Unlock()
		m := s.newMessage("writer", "#room", text, nil)
		s.recordLocked(&m)
		return m
	}
	if !write("first").Persisted {
		t.Fatal("initial append failed")
	}
	// The descriptor still points to a writable file, but creating a replacement
	// in the original directory must fail. Preserve the moved durable file.
	moved := dir + "-moved"
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	second := write("second")
	if !second.Persisted || s.histFile != nil || s.persistenceError == "" {
		t.Fatal("compaction failure did not stop further appends")
	}
	data, err := os.ReadFile(filepath.Join(moved, "history"))
	if err != nil || !strings.Contains(string(data), second.ID) {
		t.Fatal("synced acceptance lost", err)
	}
	third := write("third")
	if third.Persisted {
		t.Fatal("memory-only acceptance reported persisted")
	}
	if _, ok := s.history.message(third.ID); !ok {
		t.Fatal("memory service stopped")
	}
}
