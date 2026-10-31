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
)

// A minimal older daemon deliberately has no STATUS, topics or mentions.
// It can also withhold HISTORY to exercise cancellation at the wire boundary.
func olderDaemon(t *testing.T, holdHistory bool) (string, <-chan struct{}) {
	t.Helper()
	return tokenDaemon(t, "MULTILINE=1", holdHistory)
}

// tokenDaemon is a daemon from before AIRC levels that advertises the given
// 005 tokens, answers every CHAT request with an empty response, and knows
// no STATUS.
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
	if report.Level != protocol.Level || report.Features["AIRC"] != "1" || report.Features["HISTORY"] != "16" || report.Process.SoftLimit == nil {
		t.Fatalf("missing diagnostics: %s", out)
	}
	if _, literal := report.Features["HISTORY_START"]; literal {
		t.Fatalf("a version-only feature is still a token of its own: %s", out)
	}
	for _, warning := range report.Warnings {
		if strings.Contains(warning, "Daemon lacks") || strings.Contains(warning, "AIRC level") {
			t.Fatalf("a current daemon lacks nothing: %s", out)
		}
	}
	text := mustCLI(t, address, "doctor", "--nick", "me")
	if !strings.Contains(text, fmt.Sprintf("Level: AIRC %d (this airc %d)", protocol.Level, protocol.Level)) || !strings.Contains(text, "Features: AIRC=1 ") {
		t.Fatalf("doctor text = %s", text)
	}
}

func TestDoctorRemainsUsableAgainstOlderDaemon(t *testing.T) {
	agentEnv(t)
	address, closed := olderDaemon(t, false)
	out := mustCLI(t, address, "doctor", "--json")
	var report doctorReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	if !report.Connected || report.Server != nil || report.Level != 0 || report.Features["MULTILINE"] != "1" {
		t.Fatalf("older daemon diagnostics = %s", out)
	}
	// One warning names everything the table says the daemon lacks, and one
	// that the daemon's build is unknown; the table's names are what the
	// commands themselves refuse with.
	if len(report.Warnings) != 2 || !strings.HasPrefix(report.Warnings[0], "Daemon lacks BOT_REPLIES, CHANNELS, CHANNEL_OPERATORS, CHARTER, CHAT, ") || !strings.Contains(report.Warnings[0], fmt.Sprintf(", STATUS_POSTS, TOPIC (AIRC level 0, this airc expects %d)", protocol.Level)) || strings.Contains(report.Warnings[0], "MULTILINE") || !strings.Contains(report.Warnings[1], "predates STATUS") {
		t.Fatalf("older daemon warnings = %q", report.Warnings)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("doctor left its connection open")
	}
}

func TestCheckWaitDeadlineCancelsHistoryAndReleasesCursorLock(t *testing.T) {
	agentEnv(t)
	address, closed := olderDaemon(t, true)
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
