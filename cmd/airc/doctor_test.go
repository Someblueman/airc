package main

import (
	"bufio"
	"encoding/json"
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
				fmt.Fprint(conn, ":server 766 observer :Ephemeral session\r\n:server 005 observer MULTILINE=1 :supported\r\n:server 001 observer :Welcome\r\n")
			case "OBSERVE":
				for _, target := range strings.Split(command.Params[0], ",") {
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
	if report.Features["HISTORY_START"] != "1" || report.Process.SoftLimit == nil {
		t.Fatalf("missing diagnostics: %s", out)
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
	if !report.Connected || report.Server != nil || len(report.Warnings) < 4 || report.Features["MULTILINE"] != "1" {
		t.Fatalf("older daemon diagnostics = %s", out)
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
	if _, _, err := cli(t, address, "check", "--nick", "me", "--wait", "150ms"); err != nil {
		t.Fatal(err)
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
