package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestBlockedHistoryAppendAllowsQueriesButWithholdsSendReceipt(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	// Fill a real pipe without blocking; it acts as a stalled history device.
	total := 0
	raw, err := writer.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	if err := raw.Control(func(fd uintptr) {
		for _, block := range [][]byte{make([]byte, 4096), make([]byte, 1)} {
			for {
				n, writeErr := syscall.Write(int(fd), block)
				if n > 0 {
					total += n
				}
				if errors.Is(writeErr, syscall.EAGAIN) {
					break
				}
				if writeErr != nil {
					err = writeErr
					return
				}
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	// The deferred writer.Close above runs before the helper's Shutdown cleanup.
	address, _ := startServer(t, Config{HistoryLimit: 16}, func(s *Server) error {
		s.histFile = writer
		return nil
	})
	sender, err := irc.Dial(irc.Config{Nick: "writer", Addr: address, Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	if err := sender.Send("#room", "committed"); err != nil {
		t.Fatal(err)
	}
	// Registration and a history query must complete while the append is stuck.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	query, err := irc.DialContext(ctx, irc.Config{Nick: "reader", Addr: address, Ephemeral: true})
	if err != nil {
		t.Fatalf("history I/O blocked registration: %v", err)
	}
	defer query.Close()
	found := false
	for !found {
		if err := query.History("#room", 1); err != nil {
			t.Fatal(err)
		}
		for {
			select {
			case event := <-query.Events():
				if _, ok := event.(*irc.HistoryEvent); ok {
					found = true
				}
				if _, ok := event.(*irc.EndOfHistoryEvent); ok {
					goto nextQuery
				}
			case <-ctx.Done():
				t.Fatal("history I/O blocked a query")
			}
		}
	nextQuery:
	}
	for len(sender.Events()) > 0 {
		if _, ok := (<-sender.Events()).(*irc.SendReceiptEvent); ok {
			t.Fatal("send was confirmed before its history append")
		}
	}
	drained := make(chan Message, 1)
	go func() {
		io.CopyN(io.Discard, reader, int64(total))
		line, _ := bufio.NewReader(reader).ReadBytes('\n')
		var message Message
		json.Unmarshal(line, &message)
		drained <- message
	}()
	for {
		select {
		case event := <-sender.Events():
			if receipt, ok := event.(*irc.SendReceiptEvent); ok {
				if receipt.Message != "committed" {
					t.Fatal("wrong send receipt")
				}
				var message Message
				select {
				case message = <-drained:
				case <-ctx.Done():
					t.Fatal("history record was not readable after the send receipt")
				}
				if message.Body != "committed" || message.ID != receipt.ID {
					t.Fatal("receipt did not match the appended record")
				}
				return
			}
		case <-ctx.Done():
			t.Fatal("send did not resume after history I/O recovered")
		}
	}
}

// A store whose failed save is only logged (topics here) still shows in
// STATUS, named, until it saves again; another store's success does not
// clear it.
func TestStatusReportsAStoreThatCannotSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "topics.json")
	address, srv := startServer(t, Config{HistoryLimit: 8, PingInterval: time.Hour, ReadTimeout: time.Hour}, func(s *Server) error { return s.RestoreTopics(path) })
	conn, reader := rawConn(t, address)
	io.WriteString(conn, "NICK setter\r\nUSER u 0 * :u\r\nJOIN #hq\r\n")
	expectLine(t, reader, conn, " 366 ")
	status := func() string {
		io.WriteString(conn, "STATUS\r\n")
		line := expectLine(t, reader, conn, " 770 ")
		_, payload, _ := strings.Cut(strings.TrimRight(line, "\r\n"), " :")
		var status protocol.ServerStatus
		if err := json.Unmarshal([]byte(payload), &status); err != nil {
			t.Fatal(err, line)
		}
		return status.PersistenceError
	}
	setTopic := func(text string) {
		io.WriteString(conn, "TOPIC #hq :"+text+"\r\n")
		expectLine(t, reader, conn, " 332 ")
	}
	setTopic("saved")
	if failed := status(); failed != "" {
		t.Fatalf("persistence error before any failure: %q", failed)
	}

	// The headers' directory goes away, as a mount might.
	srv.mu.Lock()
	srv.topicsAt = filepath.Join(dir, "gone", "topics.json")
	srv.mu.Unlock()
	setTopic("unsaved")
	failed := status()
	if !strings.HasPrefix(failed, "topics: ") || !strings.Contains(failed, "gone") {
		t.Fatalf("persistence error after a failed topics save: %q", failed)
	}
	srv.mu.Lock()
	srv.env.saved("profiles", nil)
	srv.mu.Unlock()
	if again := status(); again != failed {
		t.Fatalf("another store's save cleared the topics failure: %q", again)
	}

	srv.mu.Lock()
	srv.topicsAt = path
	srv.mu.Unlock()
	setTopic("saved again")
	if failed := status(); failed != "" {
		t.Fatalf("persistence error after topics saved again: %q", failed)
	}
	if data, err := os.ReadFile(path); err != nil || !strings.Contains(string(data), "saved again") {
		t.Fatalf("topics file %q: %v", data, err)
	}
}
