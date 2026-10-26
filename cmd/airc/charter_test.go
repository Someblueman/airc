package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A charter is set by an admin or an operator, printed by charter and room,
// shown by check once and again when it changes, like the header, and noted
// by the digest.
func TestCharterIsShownOnceAndAgainWhenItChanges(t *testing.T) {
	address, token := chatServerWithToken(t, 64)
	file := filepath.Join(t.TempDir(), "charter.md")
	if err := os.WriteFile(file, []byte("# Rules\n\nOne thread per task.\nNo secrets.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	deniedCLI(t, address, "only a room operator or an admin may set the charter", "room", "#room", "--nick", "alice", "--as-operator", "--charter", file)
	if out := mustCLI(t, address, "room", "#room", "--charter", file, "--token-file", token); !strings.Contains(out, "charter:\n  # Rules\n  \n  One thread per task.\n  No secrets.") {
		t.Fatalf("room after setting:\n%s", out)
	}
	if out := mustCLI(t, address, "charter", "#room"); out != "# Rules\n\nOne thread per task.\nNo secrets.\n" {
		t.Fatalf("charter prints %q", out)
	}
	if out := mustCLI(t, address, "charter", "#room", "--json"); !strings.Contains(out, `"type":"charter"`) || !strings.Contains(out, `"charter":"# Rules\n\nOne thread per task.\nNo secrets."`) {
		t.Fatalf("charter --json prints %q", out)
	}
	send(t, address, "writer", "#room", "hello")
	check := func(extra ...string) string {
		return mustCLI(t, address, append([]string{"check", "--nick", "me", "--channel", "#room"}, extra...)...)
	}
	first := check()
	want := "#room charter: # Rules\n#room charter: \n#room charter: One thread per task.\n#room charter: No secrets.\n"
	if !strings.HasPrefix(first, want) || !strings.Contains(first, "]: hello") {
		t.Fatalf("the first check should lead with the charter:\n%s", first)
	}
	if again := check(); strings.Contains(again, "charter") {
		t.Fatalf("an unchanged charter was shown again:\n%s", again)
	}
	// An operator may change it; the digest notes the change, and check
	// shows it once more.
	mustCLI(t, address, "user", "create", "--nick", "alice")
	mustCLI(t, address, "op", "--channel", "#room", "--who", "alice", "--token-file", token)
	var stdout, stderr bytes.Buffer
	if err := run([]string{"room", "#room", "--nick", "alice", "--as-operator", "--charter", "-", "--addr", address}, strings.NewReader("Rules v2\n"), &stdout, &stderr); err != nil {
		t.Fatalf("charter from stdin: %v %s", err, stderr.String())
	}
	if digest := mustCLI(t, address, "digest", "--nick", "me", "--channel", "#room"); !strings.Contains(digest, `Charter of #room changed: "Rules v2"`) {
		t.Fatalf("digest:\n%s", digest)
	}
	lines := strings.Split(strings.TrimSpace(check("--json")), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"type":"charter"`) || !strings.Contains(lines[0], `"charter":"Rules v2"`) || strings.Contains(lines[0], `"topic"`) {
		t.Fatalf("json charter row = %v", lines)
	}
	if done := check(); strings.Contains(done, "charter") {
		t.Fatalf("charter repeated after being seen:\n%s", done)
	}
	mustCLI(t, address, "room", "#room", "--nick", "alice", "--as-operator", "--charter", "")
	if out := mustCLI(t, address, "charter", "#room"); out != "#room has no charter\n" {
		t.Fatalf("after clearing: %q", out)
	}
	if cleared := check(); strings.Contains(cleared, "charter") {
		t.Fatalf("clearing a charter should print nothing: %q", cleared)
	}
	long := strings.Repeat("x", 4097)
	if err := os.WriteFile(file, []byte(long), 0o600); err != nil {
		t.Fatal(err)
	}
	deniedCLI(t, address, "at most 4096 bytes", "room", "#room", "--charter", file, "--token-file", token)
	deniedCLI(t, address, "--charter is for room", "pins", "#room", "--charter", file)
}
