package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/pkg/fallback"
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

func TestRateLimitCarriesTheServersDelay(t *testing.T) {
	// Native IRC hands the numeric over directly; a fallback carrier wraps
	// it in a refusal. Both carry the stated delay as a number.
	native := &irc.RejectedError{Code: "486", Message: "Slow mode: retry after 12 seconds"}
	for _, err := range []error{native, &fallback.Refusal{Code: "486", UpstreamCode: 486, Err: native}} {
		e := failure(err, "send")
		if e.Code != "rate_limited" || !e.Retryable || e.RetryAfterSeconds != 12 {
			t.Fatalf("%+v", e)
		}
		if data, _ := json.Marshal(e); !strings.Contains(string(data), `"retry_after_seconds":12`) {
			t.Fatal(string(data))
		}
	}
	// A delay the server did not state in its one grammar is not invented.
	for _, text := range []string{"unknown grammar", "Slow mode: retry after 0 seconds", "Slow mode: retry after +3 seconds", "Slow mode: retry after 3 seconds later", "Slow mode: retry after many seconds"} {
		e := failure(&irc.RejectedError{Code: "486", Message: text}, "send")
		if e.Code != "rate_limited" || !e.Retryable || e.RetryAfterSeconds != 0 {
			t.Fatalf("%q: %+v", text, e)
		}
		if data, _ := json.Marshal(e); strings.Contains(string(data), "retry_after_seconds") {
			t.Fatal(string(data))
		}
	}
	if e := failure(&irc.RejectedError{Code: "437", Message: "Slow mode: retry after 12 seconds"}, "send"); e.RetryAfterSeconds != 0 {
		t.Fatalf("a delay on another numeric was trusted: %+v", e)
	}
}

func TestUnwritableStateDirIsNotReportedAsServerOutage(t *testing.T) {
	agentEnv(t)
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AIRC_STATE_DIR", filepath.Join(blocker, "state"))
	_, stderr, err := cli(t, "127.0.0.1:1", "check", "--nick", "me", "--json")
	if err == nil {
		t.Fatal("check succeeded without a state directory")
	}
	var status commandFailure
	if err := json.Unmarshal([]byte(strings.SplitN(stderr, "\n", 2)[0]), &status); err != nil {
		t.Fatal(err, stderr)
	}
	if status.Code != "state_unavailable" || status.Retryable {
		t.Fatalf("wrong failure: %+v", status)
	}
}

func TestEverySubcommandReportsJSONFailures(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	for _, args := range [][]string{
		{"history", "#room", "--limit", "0", "--json"},
		{"search", "", "--json"},
		{"thread", "not-an-id", "--json"},
		{"directory", "--who", "nobody", "--clear", "--json"},
		{"channels", "extra", "--json"},
	} {
		out, stderr, err := cli(t, address, args...)
		var status commandFailure
		if err == nil || json.Unmarshal([]byte(stderr), &status) != nil || status.Type != "error" || status.Code == "" || status.Message == "" {
			t.Errorf("airc %s: want one JSON error on stderr, got out=%q stderr=%q err=%v", strings.Join(args, " "), out, stderr, err)
		}
	}
	// A dead server is retryable for any command, and is reported exactly once.
	_, stderr, err := cli(t, "127.0.0.1:1", "history", "#room", "--json")
	var status commandFailure
	if err == nil || json.Unmarshal([]byte(stderr), &status) != nil || status.Code != "server_unavailable" || !status.Retryable {
		t.Fatalf("unreachable server: %q %v", stderr, err)
	}
	if _, stderr, _ := cli(t, "127.0.0.1:1", "check", "--nick", "me", "--json"); strings.Count(stderr, `"type":"error"`) != 1 {
		t.Fatalf("check failure reported more than once: %q", stderr)
	}
	// Without --json, errors stay plain text for people.
	if _, stderr, err := cli(t, address, "history", "#room", "--limit", "0"); err == nil || strings.Contains(stderr, `"type"`) {
		t.Fatalf("plain failure became JSON: %q", stderr)
	}
}

// Every request loop must report a lost connection as retryable, whichever
// command it serves, so agents retry rather than treat it as a bad request.
func TestLostConnectionIsRetryable(t *testing.T) {
	agentEnv(t)
	client, err := irc.Dial(irc.Config{Nick: "lost", Addr: cliTestServer(t), Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	_ = client.Close()
	err = awaitEvent(context.Background(), client, time.Minute, "reading the topic", "", func(irc.Event) (bool, error) { return false, nil })
	for _, err := range []error{err, disconnectError("server disconnected reading directory")} {
		if f := failure(err, "test"); f.Code != "server_unavailable" || !f.Retryable || f.Message != err.Error() {
			t.Errorf("%v: got code=%s retryable=%t message=%q", err, f.Code, f.Retryable, f.Message)
		}
	}
	timeout := timeoutError("timed out waiting for agent list")
	if f := failure(timeout, "test"); f.Code != "timeout" || !f.Retryable || f.Message != timeout.Error() {
		t.Errorf("timeout: got code=%s retryable=%t message=%q", f.Code, f.Retryable, f.Message)
	}
}
