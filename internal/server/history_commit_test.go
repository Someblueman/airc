package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/atomicfile"
	"github.com/Someblueman/airc/internal/testwait"
)

// gatedSync holds every history sync until release is closed, then fails
// them with err (nil syncs normally).
type gatedSync struct {
	release chan struct{}
	once    sync.Once
	err     error
	calls   atomic.Int32
}

func (g *gatedSync) open() { g.once.Do(func() { close(g.release) }) }

func (g *gatedSync) sync(file *os.File, mode atomicfile.Sync) error {
	g.calls.Add(1)
	<-g.release
	if g.err != nil {
		return g.err
	}
	return atomicfile.File(file, mode)
}

func persistedServer(t *testing.T, gate *gatedSync) (string, *Server, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "history.jsonl")
	address, srv := startServer(t, Config{HistoryLimit: 100, PingInterval: time.Hour, ReadTimeout: time.Hour}, func(s *Server) error {
		s.commit.syncFile = gate.sync
		return s.RestoreHistory(path)
	})
	t.Cleanup(gate.open) // before the server's shutdown, even on failure
	return address, srv, path
}

func registered(t *testing.T, address, nick string) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, reader := rawConn(t, address)
	io.WriteString(conn, "NICK "+nick+"\r\nUSER u 0 * :u\r\n")
	expectLine(t, reader, conn, " 422 ") // the end of the registration burst
	return conn, reader
}

func (s *Server) appendedRecords() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.histRecords
}

// expectSilence fails if anything arrives on reader within d.
func expectSilence(t *testing.T, reader *bufio.Reader, conn net.Conn, d time.Duration) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(d))
	if line, err := reader.ReadString('\n'); err == nil {
		t.Fatalf("received %q before its history sync", line)
	}
	_ = conn.SetReadDeadline(time.Time{})
}

func TestGroupCommitBroadcastsAfterSharedSyncInOrder(t *testing.T) {
	gate := &gatedSync{release: make(chan struct{})}
	address, srv, path := persistedServer(t, gate)
	var senders []net.Conn
	for i := range 6 {
		conn, reader := registered(t, address, fmt.Sprintf("s%d", i))
		io.WriteString(conn, "JOIN #room\r\n")
		expectLine(t, reader, conn, " 366 ")
		senders = append(senders, conn)
	}
	watcher, watch := registered(t, address, "watcher")
	io.WriteString(watcher, "JOIN #room\r\n")
	expectLine(t, watch, watcher, " 366 ")

	// The first post's sync is held; the rest are written meanwhile and
	// wait for the next sync, which they share.
	io.WriteString(senders[0], "PRIVMSG #room :first\r\n")
	testwait.Until(t, 5*time.Second, "the first sync", func() bool { return gate.calls.Load() == 1 })
	for i, conn := range senders[1:] {
		io.WriteString(conn, fmt.Sprintf("PRIVMSG #room :later %d\r\n", i))
	}
	testwait.Until(t, 5*time.Second, "every post written", func() bool { return srv.appendedRecords() == len(senders) })
	expectSilence(t, watch, watcher, 150*time.Millisecond)
	gate.open()

	var received []string
	for range senders {
		line := expectLine(t, watch, watcher, "PRIVMSG #room")
		id, _, _ := strings.Cut(strings.TrimPrefix(line, "@msgid="), ";")
		received = append(received, id)
	}
	if calls := gate.calls.Load(); calls != 2 {
		t.Fatalf("%d syncs for %d posts, want the first and one shared", calls, len(senders))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored []string
	var last uint64
	for line := range strings.Lines(string(data)) {
		var message Message
		if err := json.Unmarshal([]byte(line), &message); err != nil {
			t.Fatal(err)
		}
		if message.Seq <= last {
			t.Fatalf("history file out of order: %d after %d", message.Seq, last)
		}
		last = message.Seq
		stored = append(stored, message.ID)
	}
	if fmt.Sprint(received) != fmt.Sprint(stored) {
		t.Fatalf("broadcast order %v, file order %v", received, stored)
	}
}

func TestGroupCommitFailedSyncReportsEveryPostInIt(t *testing.T) {
	gate := &gatedSync{release: make(chan struct{}), err: errors.New("injected sync failure")}
	address, srv, _ := persistedServer(t, gate)
	type sender struct {
		conn   net.Conn
		reader *bufio.Reader
	}
	var senders []sender
	for i := range 4 {
		conn, reader := registered(t, address, fmt.Sprintf("s%d", i))
		senders = append(senders, sender{conn, reader})
	}
	io.WriteString(senders[0].conn, "PRIVMSG absent :first\r\n")
	testwait.Until(t, 5*time.Second, "the first sync", func() bool { return gate.calls.Load() == 1 })
	for _, s := range senders[1:] {
		io.WriteString(s.conn, "PRIVMSG absent :later\r\n")
	}
	testwait.Until(t, 5*time.Second, "every post written", func() bool { return srv.appendedRecords() == len(senders) })
	expectSilence(t, senders[0].reader, senders[0].conn, 100*time.Millisecond)
	gate.open()
	for i, s := range senders {
		receipt := receiptFromLine(t, expectLine(t, s.reader, s.conn, " 762 "))
		if !receipt.Receipt.Accepted || receipt.Receipt.Persisted {
			t.Fatalf("post %d: %+v", i, receipt.Receipt)
		}
	}
	// The broken file is closed; later posts are served from memory.
	io.WriteString(senders[0].conn, "PRIVMSG absent :after\r\n")
	if receipt := receiptFromLine(t, expectLine(t, senders[0].reader, senders[0].conn, " 762 ")); receipt.Receipt.Persisted {
		t.Fatal("a post after the failure reported persisted")
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.histFile != nil || !strings.Contains(srv.persistenceError, "injected") {
		t.Fatalf("file %v, persistence error %q", srv.histFile, srv.persistenceError)
	}
}

// Many concurrent posts against tiny segments seal the file while others
// wait for their sync; a restart must find the newest posts, in order, and
// every one reported persisted, and the archive must hold all of them.
func TestGroupCommitRotatesUnderConcurrentPosts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	srv := New(Config{HistoryLimit: 4, PingInterval: time.Hour, ReadTimeout: time.Hour})
	srv.histSegment = 2048
	if err := srv.RestoreHistory(path); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(listener) }()
	address := listener.Addr().String()
	var mu sync.Mutex
	var persisted []string
	var wg sync.WaitGroup
	for i := range 12 {
		conn, reader := registered(t, address, fmt.Sprintf("s%d", i))
		wg.Go(func() {
			for j := range 15 {
				io.WriteString(conn, fmt.Sprintf("PRIVMSG absent :%d-%d\r\n", i, j))
				line := expectLine(t, reader, conn, " 762 ")
				if receipt := receiptFromLine(t, line); receipt.Receipt.Persisted {
					mu.Lock()
					persisted = append(persisted, receipt.ID)
					mu.Unlock()
				}
			}
		})
	}
	wg.Wait()
	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-served; err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 12*15 {
		t.Fatalf("%d of %d posts persisted", len(persisted), 12*15)
	}
	restored := New(Config{HistoryLimit: 4})
	if err := restored.RestoreHistory(path); err != nil {
		t.Fatal(err)
	}
	defer restored.Shutdown(context.Background())
	if restored.history.size != 4 {
		t.Fatalf("restored %d messages", restored.history.size)
	}
	for i := range restored.history.size {
		if got := restored.history.at(i).Seq; got != uint64(12*15-3+i) {
			t.Fatalf("restored message %d has seq %d", i, got)
		}
	}
	sealed, _ := filepath.Glob(path + ".????????????????????")
	if len(sealed) < 2 {
		t.Fatalf("%d sealed segments", len(sealed))
	}
	var seqs []uint64
	for _, file := range append(sealed, path) {
		_ = readSegment(file, 0, 1<<40, func(m Message, _, _ int64) bool {
			seqs = append(seqs, m.Seq)
			return true
		})
	}
	for i, seq := range seqs {
		if seq != uint64(i+1) {
			t.Fatalf("archive holds %d messages, out of order or missing at %d: %v", len(seqs), i, seqs)
		}
	}
	if len(seqs) != 12*15 {
		t.Fatalf("archive holds %d of %d messages", len(seqs), 12*15)
	}
}
