package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/server"
)

func TestIdleWaitAllowsConcurrentCheckAndReloadsCursor(t *testing.T) {
	agentEnv(t)
	tap := tapDaemon(t, cliTestServer(t))
	address := tap.addr
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
	tap.waitObserving(t, 1)
	mustCLI(t, address, "check", "--nick", "reader", "--channel", "room", "--json")
	sent := send(t, address, "writer", "#room", "wake up")
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

// The window keeps two messages, so the reader's cursor leaves it during the
// outage; the history file's archive resumes it without a gap.
func TestWaitReconnectsAfterDaemonOutageAndResumesFromTheArchive(t *testing.T) {
	agentEnv(t)
	first := startServerAt(t, "127.0.0.1:0", server.Config{HistoryLimit: 2}, filepath.Join(t.TempDir(), "history"))
	tap := tapDaemon(t, first.addr)
	address := tap.addr
	send(t, address, "writer", "#room", "old cursor")
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
	tap.waitObserving(t, 1)
	first.stop()
	// Keep the listener absent until the waiter has made (and failed) a reconnect
	// attempt; the proxy counts connections it could not pass to the daemon.
	tap.waitRefused(t, 1)
	// Retained history moves on while the daemon is down, before reconnecting
	// clients can register.
	first.restart(t, func(address string) {
		send(t, address, "writer", "#room", "offline one")
		send(t, address, "writer", "#room", "offline two")
	})
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatal(result.err)
		}
		if len(checkMessages(t, result.out)) != 2 || len(checkFooter(t, result.out).Gaps) != 0 {
			t.Fatal("reconnect missed messages or reported a gap", result.out)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("wait did not recover")
	}
}
