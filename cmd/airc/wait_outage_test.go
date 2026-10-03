package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/server"
)

func TestIdleWaitAllowsConcurrentCheckAndReloadsCursor(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	mustCLI(t, address, "check", "--nick", "reader", "--channel", "room", "--json")
	done := make(chan struct {
		out string
		err error
	}, 1)
	go func() {
		out, _, err := cli(t, address, "check", "--nick", "reader", "--channel", "room", "--wait", "3s", "--json")
		done <- struct {
			out string
			err error
		}{out, err}
	}()
	time.Sleep(100 * time.Millisecond)
	mustCLI(t, address, "check", "--nick", "reader", "--channel", "room", "--json")
	sent := posted(t, address, "writer", "#room", "wake up")
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatal(result.err)
		}
		messages := checkMessages(t, result.out)
		if len(messages) != 1 || messages[0].ID != sent.ID {
			t.Fatal(result.out)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("wait did not wake")
	}
	if out := mustCLI(t, address, "check", "--nick", "reader", "--channel", "room", "--json"); len(checkMessages(t, out)) != 0 {
		t.Fatal("cursor regressed", out)
	}
}

func TestWaitReconnectsAfterDaemonOutageAndReportsRetentionGap(t *testing.T) {
	agentEnv(t)
	path := filepath.Join(t.TempDir(), "history")
	var first *server.Server
	address := cliTestServerSetup(t, server.Config{HistoryLimit: 2}, func(s *server.Server) error { first = s; return s.RestoreHistory(path) })
	posted(t, address, "writer", "#room", "old cursor")
	mustCLI(t, address, "check", "--nick", "reader", "--channel", "room", "--json")
	done := make(chan struct {
		out string
		err error
	}, 1)
	go func() {
		out, _, err := cli(t, address, "check", "--nick", "reader", "--channel", "room", "--wait", "5s", "--json")
		done <- struct {
			out string
			err error
		}{out, err}
	}()
	time.Sleep(100 * time.Millisecond)
	if err := first.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Keep the listener absent long enough to require a failed reconnect attempt.
	time.Sleep(400 * time.Millisecond)
	next := server.New(server.Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), HistoryLimit: 2, ReadTimeout: time.Hour, PingInterval: time.Hour})
	if err := next.RestoreHistory(path); err != nil {
		t.Fatal(err)
	}
	// Populate retained history before reconnecting clients can register.
	seed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go next.Serve(seed)
	posted(t, seed.Addr().String(), "writer", "#room", "offline one")
	posted(t, seed.Addr().String(), "writer", "#room", "offline two")
	next.Shutdown(context.Background())
	l, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	recovered := server.New(server.Config{HistoryLimit: 2, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := recovered.RestoreHistory(path); err != nil {
		l.Close()
		t.Fatal(err)
	}
	go recovered.Serve(l)
	defer recovered.Shutdown(context.Background())
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatal(result.err)
		}
		if len(checkMessages(t, result.out)) != 2 || len(checkFooter(t, result.out).Gaps) < 1 {
			t.Fatal("reconnect missed retained messages or gap", result.out)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("wait did not recover")
	}
}
