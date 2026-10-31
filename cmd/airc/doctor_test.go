package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// olderDaemon is a daemon from before AIRC levels: one token per feature and
// no level, which a current client refuses at the dial.
func olderDaemon(t *testing.T) (string, <-chan struct{}) {
	t.Helper()
	return tokenDaemon(t, "MULTILINE=1 CHAT=1 HISTORY=8", false)
}

// stubDaemon is a daemon at this client's level that answers every CHAT
// request with an empty response and knows no STATUS. It can withhold
// HISTORY to exercise cancellation at the wire boundary.
func stubDaemon(t *testing.T, holdHistory bool) (string, <-chan struct{}) {
	t.Helper()
	return tokenDaemon(t, fmt.Sprintf("AIRC=%d HISTORY=8", protocol.Level), holdHistory)
}

// tokenDaemon is a fake daemon that advertises the given 005 tokens, answers
// every CHAT request with an empty response, and knows no STATUS.
func tokenDaemon(t *testing.T, tokens string, holdHistory bool) (string, <-chan struct{}) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			command, err := protocol.Parse(scanner.Text())
			if err != nil {
				return
			}
			switch command.Name {
			case "USER":
				fmt.Fprintf(conn, ":server 766 observer :Ephemeral session\r\n:server 005 observer %s :supported\r\n:server 001 observer :Welcome\r\n", tokens)
			case "CHAT":
				fmt.Fprint(conn, ":server 778 observer :End of chat response\r\n")
			case "OBSERVE":
				for target := range strings.SplitSeq(command.Params[0], ",") {
					fmt.Fprintf(conn, ":server 765 observer %s :Now observing\r\n", target)
				}
			case "HISTORY":
				if !holdHistory {
					fmt.Fprintf(conn, ":server 761 observer %s ok :End of history\r\n", command.Params[0])
				}
			case "STATUS":
				fmt.Fprint(conn, ":server 421 observer STATUS :Unknown command\r\n")
			case "QUIT":
				return
			}
		}
	}()
	t.Cleanup(func() { listener.Close() })
	return listener.Addr().String(), closed
}

func TestDoctorReportsCapabilitiesRetentionAndLockOwner(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	store, err := openCursors(options{addr: address}, "me")
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	out := mustCLI(t, address, "doctor", "--nick", "me", "--json")
	var report doctorReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	if !report.Connected || !report.Ephemeral || report.Server == nil || report.Server.HistoryLimit != 16 || report.Server.PID != os.Getpid() {
		t.Fatalf("doctor report = %s", out)
	}
	if report.Cursor == nil || !report.Cursor.Locked || report.Cursor.Owner == nil || report.Cursor.Owner.PID != os.Getpid() {
		t.Fatalf("missing lock ownership: %s", out)
	}
	if report.Level != protocol.Level || report.ClientLevel != protocol.Level || report.Features["AIRC"] != fmt.Sprint(protocol.Level) || report.Features["HISTORY"] != "16" || report.Process.SoftLimit == nil {
		t.Fatalf("missing diagnostics: %s", out)
	}
	if _, literal := report.Features["HISTORY_START"]; literal {
		t.Fatalf("a version-only feature is still a token of its own: %s", out)
	}
	for _, warning := range report.Warnings {
		if strings.Contains(warning, "AIRC level") {
			t.Fatalf("a current daemon lacks nothing: %s", out)
		}
	}
	text := mustCLI(t, address, "doctor", "--nick", "me")
	if !strings.Contains(text, fmt.Sprintf("Level: AIRC %d (this airc %d)", protocol.Level, protocol.Level)) || !strings.Contains(text, fmt.Sprintf("Features: AIRC=%d ", protocol.Level)) {
		t.Fatalf("doctor text = %s", text)
	}
}

// doctor still reports a daemon below this client's level: the dial reads
// its level before refusing it, and the report says both levels and why.
func TestDoctorReportsADaemonBelowTheClientsLevel(t *testing.T) {
	agentEnv(t)
	address, closed := olderDaemon(t)
	out, _, err := cli(t, address, "doctor", "--json")
	if !errors.Is(err, irc.ErrDaemonTooOld) {
		t.Fatalf("doctor against an older daemon = %v", err)
	}
	var report doctorReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err, out)
	}
	refusal := (&irc.DaemonTooOldError{Need: protocol.Level}).Error()
	if report.Connected || report.Level != 0 || report.ClientLevel != protocol.Level || report.Fallback != "" || !strings.HasPrefix(report.Error, refusal) || !strings.Contains(report.Error, "never restart it yourself") {
		t.Fatalf("older daemon report = %s", out)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("doctor left its connection open")
	}
	address, _ = olderDaemon(t)
	text, _, err := cli(t, address, "doctor")
	if !errors.Is(err, irc.ErrDaemonTooOld) || !strings.Contains(text, fmt.Sprintf("Level: AIRC 0 (this airc %d)\n", protocol.Level)) || !strings.Contains(text, "connected: false") {
		t.Fatalf("doctor text = %s (%v)", text, err)
	}
}

func TestCheckWaitDeadlineCancelsHistoryAndReleasesCursorLock(t *testing.T) {
	agentEnv(t)
	address, closed := stubDaemon(t, true)
	start := time.Now()
	if _, _, err := cli(t, address, "check", "--nick", "me", "--wait", "150ms"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected history timeout, got %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("history wait ignored the total deadline")
	}
	store, err := openCursors(options{addr: address}, "me")
	if err != nil {
		t.Fatalf("cursor lock leaked: %v", err)
	}
	store.close()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("cancelled check left its socket open")
	}
}
