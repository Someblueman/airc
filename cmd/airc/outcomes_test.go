package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestCheckRegistrationTimeoutIsJSONFailure(t *testing.T) {
	agentEnv(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, err := l.Accept()
		if err == nil {
			defer c.Close()
			var buf [4096]byte
			for {
				if _, err := c.Read(buf[:]); err != nil {
					return
				}
			}
		}
	}()
	out, stderr, err := cli(t, l.Addr().String(), "check", "--nick", "me", "--wait", "150ms", "--json")
	if !errors.Is(err, context.DeadlineExceeded) || out != "" {
		t.Fatalf("timeout became success: %q %v", out, err)
	}
	var status commandFailure
	if err := json.Unmarshal([]byte(stderr), &status); err != nil {
		t.Fatal(err, stderr)
	}
	if status.Code != "timeout" || status.Phase != "login" || !status.Retryable {
		t.Fatalf("wrong failure: %+v", status)
	}
	<-done
}

func TestCompletedCheckAlwaysHasOutcome(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	for _, wait := range []string{"0", "80ms"} {
		out := mustCLI(t, address, "check", "--nick", "me", "--wait", wait, "--json")
		expected := "no_messages"
		if wait != "0" {
			expected = "wait_expired"
		}
		if len(checkBodies(t, out)) != 0 || checkFooter(t, out).Code != expected {
			t.Fatal(out)
		}
	}
}

func TestJSONErrorsAreClassifiedWithoutParsingProse(t *testing.T) {
	agentEnv(t)
	address := cliTestServerWith(t, server.Config{HistoryLimit: 16})
	for _, args := range [][]string{
		{"send", "--nick", "me", "--channel", "bad room", "--message", "x", "--json"},
		{"check", "--nick", "me", "--json", "--unknown-flag"},
		{"check", "--nick", "me", "--unknown-flag", "--json"},
	} {
		_, stderr, err := cli(t, address, args...)
		if err == nil {
			t.Fatal("accepted invalid input")
		}
		var failure commandFailure
		if err := json.Unmarshal([]byte(stderr), &failure); err != nil {
			t.Fatal(err, stderr)
		}
		if failure.Type != "error" || failure.Retryable || failure.Code == "confirmation_unknown" {
			t.Fatalf("wrong outcome: %+v", failure)
		}
	}
	e := failure(&irc.RejectedError{Code: "904", Message: "arbitrary translated text"}, "login")
	if e.Code != "auth_failed" || e.Retryable || strings.Contains(e.Code, "text") {
		t.Fatal(e)
	}
}
