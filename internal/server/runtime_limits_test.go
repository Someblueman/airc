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

	"github.com/Someblueman/airc/internal/protocol"
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

func TestCompactionFailureKeepsAppendingAndRetriesLater(t *testing.T) {
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
	if !second.Persisted || s.histFile == nil || s.persistenceError == "" {
		t.Fatal("a failed compaction must be reported without closing the valid history file")
	}
	third := write("third")
	if !third.Persisted {
		t.Fatal("appends stopped after a compaction failure")
	}
	data, err := os.ReadFile(filepath.Join(moved, "history"))
	if err != nil || !strings.Contains(string(data), second.ID) || !strings.Contains(string(data), third.ID) {
		t.Fatal("synced acceptance lost", err)
	}
	// Once the fault clears, the next attempt compacts and clears the error.
	if err := os.Rename(moved, dir); err != nil {
		t.Fatal(err)
	}
	for range 20 {
		write("later")
	}
	if s.persistenceError != "" || s.histRecords > 4 {
		t.Fatalf("compaction did not recover: %q, %d records", s.persistenceError, s.histRecords)
	}
}

func TestShutdownHonoursDeadlineWhilePostIsStalled(t *testing.T) {
	s := New(Config{HistoryLimit: 3})
	s.messageMu.Lock() // a post stuck in its history write
	defer s.messageMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Shutdown(ctx) }()
	select {
	case err := <-done:
		if err != nil && err != context.DeadlineExceeded {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown ignored its deadline while a post held messageMu")
	}
}

func TestHandlerPanicIsConfinedToItsSession(t *testing.T) {
	s := New(Config{HistoryLimit: 3})
	client := &session{server: s, client: Client{ID: "broken"}}
	// Handlers hold both locks through deferred unlocks, as handle does.
	ok := s.recovering(client, "TEST", func() {
		s.messageMu.Lock()
		defer s.messageMu.Unlock()
		s.mu.Lock()
		defer s.mu.Unlock()
		panic("handler bug")
	})
	if ok {
		t.Fatal("panicking handler reported success")
	}
	if !s.mu.TryLock() {
		t.Fatal("mu left locked after a handler panic")
	}
	s.mu.Unlock()
	if !s.messageMu.TryLock() {
		t.Fatal("messageMu left locked after a handler panic")
	}
	s.messageMu.Unlock()
}

func TestOnlyReadsAndSignalsSkipThePostLock(t *testing.T) {
	for line, want := range map[string]bool{
		"PRIVMSG #room :hello":             true,
		"REACT abc :seen":                  true,
		`CHAT :{"action":"correct"}`:       true,
		`CHAT :{"action":"poll"}`:          true,
		`CHAT :{"action":"vote"}`:          true,
		`CHAT :{"action":"pin"}`:           true,
		`CHAT :{"action":"room"}`:          true,
		`CHAT :{"action":"something-new"}`: true,
		`CHAT :not json`:                   true,
		`CHAT :{"action":"context"}`:       false,
		`CHAT :{"action":"pins"}`:          false,
		`CHAT :{"action":"waiting"}`:       false,
		`CHAT :{"action":"typing"}`:        false,
		"HISTORY #room":                    false,
		"CHECK :{}":                        false,
	} {
		if got := serializesPosts(protocolCommand(t, line)); got != want {
			t.Errorf("%s: serialized=%v, want %v", line, got, want)
		}
	}
}

func protocolCommand(t *testing.T, line string) protocol.Command {
	t.Helper()
	command, err := protocol.Parse(line)
	if err != nil {
		t.Fatal(err)
	}
	return command
}
