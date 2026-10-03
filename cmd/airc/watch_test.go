package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/server"
)

// syncBuffer is a writer the test can read while the watcher is still writing.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func waitForOutput(t *testing.T, out *syncBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for !strings.Contains(out.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %q in watcher output:\n%s", want, out.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// startWatch runs the watcher until the test ends.
func startWatch(t *testing.T, args ...string) (*syncBuffer, func() error) {
	t.Helper()
	out := &syncBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runWatchContext(ctx, args, out, io.Discard) }()
	var once sync.Once
	var result error
	stop := func() error {
		once.Do(func() {
			cancel()
			select {
			case result = <-done:
			case <-time.After(5 * time.Second):
				t.Error("watcher did not stop after cancellation")
			}
		})
		return result
	}
	t.Cleanup(func() { _ = stop() })
	return out, stop
}

type restartableServer struct {
	srv  *server.Server
	done chan error
}

func startServerAt(t *testing.T, address string, cfg server.Config, historyFile string) (*restartableServer, string) {
	t.Helper()
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	cfg.PingInterval, cfg.ReadTimeout = time.Hour, time.Hour
	cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.New(cfg)
	if historyFile != "" {
		if err := srv.RestoreHistory(historyFile); err != nil {
			t.Fatal(err)
		}
	}
	s := &restartableServer{srv: srv, done: make(chan error, 1)}
	go func() { s.done <- srv.Serve(listener) }()
	t.Cleanup(func() { s.stop(t) })
	return s, listener.Addr().String()
}

func (s *restartableServer) stop(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = s.srv.Shutdown(ctx)
	select {
	case <-s.done:
	case <-time.After(2 * time.Second):
	}
}

func TestWatchShowsBacklogThenLiveMessages(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	for _, body := range []string{"m1", "m2", "m3", "m4"} {
		send(t, address, "writer", "#room", body)
	}
	out, stop := startWatch(t, "--channel", "#room", "--color", "never", "--width", "100", "--backlog", "2", "--addr", address)
	waitForOutput(t, out, "--- live ---")
	got := out.String()
	if strings.Contains(got, "m1") || strings.Contains(got, "m2") || !strings.Contains(got, "m3") || !strings.Contains(got, "m4") {
		t.Fatalf("backlog should be the newest two messages:\n%s", got)
	}
	if strings.Index(got, "m4") > strings.Index(got, "--- live ---") {
		t.Fatalf("backlog must come before the live marker:\n%s", got)
	}
	send(t, address, "writer", "#room", "m5")
	waitForOutput(t, out, "m5")
	if strings.Count(out.String(), "m4") != 1 || strings.Count(out.String(), "m5") != 1 {
		t.Fatalf("a message was shown twice:\n%s", out.String())
	}
	if err := stop(); err != nil {
		t.Fatalf("clean shutdown returned %v", err)
	}
}

func TestWatchJSONReplaysNothingUnlessAsked(t *testing.T) {
	agentEnv(t)
	tap := tapDaemon(t, cliTestServer(t))
	address := tap.addr
	send(t, address, "writer", "#room", "already-said")

	quiet, _ := startWatch(t, "--channel", "#room", "--json", "--addr", address)
	// Once the daemon acknowledges the observation, a single live message must
	// reach the watcher; no resend loop is needed.
	tap.waitObserving(t, 1)
	send(t, address, "writer", "#room", "fresh")
	waitForOutput(t, quiet, "fresh")
	if strings.Contains(quiet.String(), "already-said") {
		t.Fatalf("JSON mode replayed history without --backlog:\n%s", quiet.String())
	}

	replay, _ := startWatch(t, "--channel", "#room", "--json", "--backlog", "1", "--addr", address)
	waitForOutput(t, replay, `"type":"message"`)
	if strings.Contains(replay.String(), `"type":"history"`) {
		t.Fatalf("backlog should use the live message shape:\n%s", replay.String())
	}
}

func TestWatchShowsDirectMessagesSentWhileNobodyWasWatching(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	mustCLI(t, address, "send", "--nick", "planner", "--to", "builder", "--message", "queued-for-builder")
	out, _ := startWatch(t, "--channel", "@Builder", "--color", "never", "--backlog", "5", "--addr", address)
	waitForOutput(t, out, "queued-for-builder")
	if !strings.Contains(out.String(), "[dm -> builder]") {
		t.Fatalf("direct message is not labelled:\n%s", out.String())
	}
}

func TestWatchResumesAfterServerRestartWithoutLossOrDuplicates(t *testing.T) {
	agentEnv(t)
	old := watchMinBackoff
	watchMinBackoff = 400 * time.Millisecond // long enough to send while the watcher is still away
	t.Cleanup(func() { watchMinBackoff = old })
	file := filepath.Join(t.TempDir(), "history.jsonl")
	cfg := server.Config{HistoryLimit: 32}

	first, address := startServerAt(t, "127.0.0.1:0", cfg, file)
	send(t, address, "writer", "#room", "msg-A")
	out, _ := startWatch(t, "--channel", "#room", "--color", "never", "--width", "100", "--backlog", "5", "--addr", address)
	waitForOutput(t, out, "--- live ---")

	first.stop(t)
	waitForOutput(t, out, "connection lost")
	_, _ = startServerAt(t, address, cfg, file)
	send(t, address, "writer", "#room", "msg-B") // sent while the watcher is still backing off
	waitForOutput(t, out, "reconnected")
	waitForOutput(t, out, "msg-B")
	send(t, address, "writer", "#room", "msg-C")
	waitForOutput(t, out, "msg-C")

	got := out.String()
	for _, body := range []string{"msg-A", "msg-B", "msg-C"} {
		if n := strings.Count(got, body); n != 1 {
			t.Errorf("%s shown %d times, want exactly once:\n%s", body, n, got)
		}
	}
	if strings.Count(got, "connection lost") != 1 {
		t.Errorf("an outage should be reported once:\n%s", got)
	}
}

func TestWatchFailsFastWhenTheServerIsNotThere(t *testing.T) {
	agentEnv(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	done := make(chan error, 1)
	go func() {
		done <- runWatchContext(context.Background(), []string{"--channel", "#room", "--addr", address}, io.Discard, io.Discard)
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error when the first connection fails")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watch should report a failed first connection instead of retrying forever")
	}
}
