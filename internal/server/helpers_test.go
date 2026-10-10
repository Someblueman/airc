package server

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// startServer builds a server from cfg, runs setup on it before Serve, and
// serves it on a loopback port. The server is shut down when the test ends;
// a Serve or Shutdown error fails the test. A nil Logger is replaced with one
// that discards output, and a nil setup is skipped.
func startServer(t *testing.T, cfg Config, setup func(*Server) error) (string, *Server) {
	t.Helper()
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	srv := New(cfg)
	if setup != nil {
		if err := setup(srv); err != nil {
			t.Fatal(err)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Error(err)
		}
		if err := <-served; err != nil {
			t.Error(err)
		}
	})
	return listener.Addr().String(), srv
}

// rawServer starts a plain server with a small history and no timeouts.
func rawServer(t *testing.T) string {
	t.Helper()
	address, _ := startServer(t, Config{HistoryLimit: 8, PingInterval: time.Hour, ReadTimeout: time.Hour}, nil)
	return address
}

// accessServer starts a server that requires the connection credential
// strings.Repeat("a", 64) before any other command.
func accessServer(t *testing.T, cfg Config) (string, *Server) {
	t.Helper()
	return startServer(t, cfg, func(s *Server) error { return s.EnableAccess(strings.Repeat("a", 64)) })
}

// adminWireServer starts a server whose operator credential is testAdminToken.
func adminWireServer(t *testing.T) (string, *Server) {
	t.Helper()
	return startServer(t, Config{HistoryLimit: 32, ReadTimeout: time.Hour, PingInterval: time.Hour}, func(s *Server) error {
		return s.EnableAdmin(testAdminToken)
	})
}

// collaborationServer starts a server with admin, accounts, chat state and
// history persisted under dir, so a second call with the same dir restores it.
func collaborationServer(t *testing.T, dir string) (string, *Server) {
	t.Helper()
	return startServer(t, Config{HistoryLimit: 64, PingInterval: time.Hour}, func(s *Server) error {
		for _, err := range []error{s.EnableAdmin(testAdminToken), s.RestoreAccounts(filepath.Join(dir, "accounts")), s.RestoreChat(filepath.Join(dir, "chat")), s.RestoreHistory(filepath.Join(dir, "history"))} {
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// eventually polls condition until it holds, failing the test with what if it
// does not within five seconds. The deadline only bounds failure.
func eventually(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func rawConn(t *testing.T, address string) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn, bufio.NewReader(conn)
}

// expectLine reads lines until one contains want, failing on timeout.
func expectLine(t *testing.T, reader *bufio.Reader, conn net.Conn, want string) string {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("waiting for %q: %v", want, err)
		}
		if strings.Contains(line, want) {
			return line
		}
	}
}

// readUntilClosed returns every line the server sends until it closes the
// connection, failing if it does not.
func readUntilClosed(t *testing.T, conn net.Conn, reader *bufio.Reader) []string {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var lines []string
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			lines = append(lines, line)
		}
		if err == io.EOF {
			return lines
		}
		if err != nil {
			t.Fatalf("connection stayed open after %q: %v", lines, err)
		}
	}
}

func countLines(lines []string, want string) int {
	n := 0
	for _, line := range lines {
		if strings.Contains(line, want) {
			n++
		}
	}
	return n
}
