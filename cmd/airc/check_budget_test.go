package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/server"
)

func checkFooter(t *testing.T, output string) checkStatus {
	t.Helper()
	var status checkStatus
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		var value checkStatus
		if err := json.Unmarshal([]byte(line), &value); err != nil {
			t.Fatal(err)
		}
		if value.Type == "status" {
			status = value
		}
	}
	return status
}

func TestCheckTotalMessageBudgetAcrossTargetsAndOverlappingInbox(t *testing.T) {
	agentEnv(t)
	address := cliTestServerWith(t, server.Config{HistoryLimit: 64})
	for i := 0; i < 9; i++ {
		channel := "#one"
		if i%2 != 0 {
			channel = "#two"
		}
		send(t, address, "writer", channel, fmt.Sprintf("@me assignment %d", i))
	}
	var received []string
	for attempt := 0; attempt < 4; attempt++ {
		out := mustCLI(t, address, "check", "--nick", "me", "--channel", "one,two", "--max-messages", "3", "--limit", "2", "--json")
		bodies := checkBodies(t, out)
		if len(bodies) > 3 {
			t.Fatalf("budget exceeded: %v", bodies)
		}
		received = append(received, bodies...)
		if attempt < 2 && !checkFooter(t, out).More {
			t.Fatalf("missing more indicator: %s", out)
		}
	}
	if len(received) != 9 {
		t.Fatalf("lost or repeated assignments: %v", received)
	}
	for i, body := range received {
		if body != fmt.Sprintf("@me assignment %d", i) {
			t.Fatalf("order or deduplication: %v", received)
		}
	}
}

func TestCheckByteBudgetKeepsWholeMessagesUnreadUntilNextCheck(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	for i := 0; i < 3; i++ {
		send(t, address, "writer", "#room", fmt.Sprint(i)+strings.Repeat("x", 550))
	}
	var received []string
	for i := 0; i < 3; i++ {
		out := mustCLI(t, address, "check", "--nick", "me", "--channel", "room", "--max-bytes", "1024", "--json")
		if len(out) > 1024 {
			t.Fatalf("output has %d bytes", len(out))
		}
		bodies := checkBodies(t, out)
		if len(bodies) != 1 || len(bodies[0]) != 551 {
			t.Fatalf("message truncated or omitted: %v", bodies)
		}
		received = append(received, bodies[0])
	}
	if received[0][0] != '0' || received[1][0] != '1' || received[2][0] != '2' {
		t.Fatalf("cursor skipped messages: %v", received)
	}
}

type brokenOutput struct{}

func (brokenOutput) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestCheckOutputFailureAndOversizedMessageDoNotConsume(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	send(t, address, "writer", "#room", strings.Repeat("x", 2000))
	args := []string{"check", "--nick", "me", "--channel", "room", "--addr", address, "--json"}
	if err := run(args, strings.NewReader(""), brokenOutput{}, io.Discard); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("output error = %v", err)
	}
	var out bytes.Buffer
	if err := run(append(args, "--max-bytes", "1024"), strings.NewReader(""), &out, io.Discard); err == nil || !strings.Contains(err.Error(), "remains unread") {
		t.Fatalf("oversized message error = %v", err)
	}
	if out.Len() != 0 {
		t.Fatal("oversized message should fail before emitting output")
	}
	if got := checkBodies(t, mustCLI(t, address, "check", "--nick", "me", "--channel", "room", "--json")); len(got) != 1 || len(got[0]) != 2000 {
		t.Fatalf("message was consumed: %v", got)
	}
}
