package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// An admin turns a room's orchestrator mode on with airc room; check shows
// the orchestrated line once and again when it changes; only the
// orchestrator hands work out there and a done step says what was
// delivered; the digest gives every agent the goal and the orchestrator the
// room's handoff counts; off ends it.
func TestRoomOrchestratorModeEndToEnd(t *testing.T) {
	address, token := chatServerWithToken(t, 64)
	mustCLI(t, address, "user", "create", "--nick", "lead", "--agent")
	mustCLI(t, address, "user", "create", "--nick", "worker", "--agent")
	mustCLI(t, address, "user", "create", "--nick", "quiet")
	send(t, address, "worker", "#work", "hello")

	deniedCLI(t, address, "only an admin may set the orchestrator; drop --as-operator", "room", "#work", "--nick", "lead", "--as-operator", "--orchestrate", "lead")
	deniedCLI(t, address, "the orchestrator must be a registered nickname; airc user create --nick ghost", "room", "#work", "--orchestrate", "ghost", "--token-file", token)
	deniedCLI(t, address, "a goal is one line of at most 400 bytes", "room", "#work", "--orchestrate", "lead", "--goal", strings.Repeat("g", 401), "--token-file", token)
	deniedCLI(t, address, "--orchestrate and --goal are for room configuration", "pins", "#work", "--orchestrate", "lead")

	// A registered nickname without a card is warned about, not refused.
	_, stderr, err := cli(t, address, "room", "#work", "--orchestrate", "quiet", "--token-file", token)
	if err != nil || !strings.Contains(stderr, "airc: warning: quiet has no directory card yet; orchestrating anyway") {
		t.Fatalf("quiet: %v %q", err, stderr)
	}
	var set irc.ChatEntry
	out := mustCLI(t, address, "room", "#work", "--orchestrate", "@lead", "--goal", "ship the parser fix", "--token-file", token, "--json")
	if err := json.Unmarshal([]byte(out), &set); err != nil || set.Action != "room" || set.Orchestrator != "lead" || set.Goal != "ship the parser fix" {
		t.Fatalf("set: %v %s", err, out)
	}
	if out := mustCLI(t, address, "room", "#work"); !strings.Contains(out, "\n#work orchestrated by lead: ship the parser fix") {
		t.Fatalf("room prints:\n%s", out)
	}

	check := func(nick string, extra ...string) string {
		return mustCLI(t, address, append([]string{"check", "--nick", nick, "--channel", "#work"}, extra...)...)
	}
	first := check("worker")
	if !strings.HasPrefix(first, "#work orchestrated by lead: ship the parser fix\n") || !strings.Contains(first, "(mentions you)") || !strings.Contains(first, "@agents lead orchestrates #work: ship the parser fix. Take lead's handoffs") {
		t.Fatalf("the first check should lead with the orchestrated line and carry the announcement:\n%s", first)
	}
	if again := check("worker"); strings.Contains(again, "orchestrated by") {
		t.Fatalf("an unchanged mode was shown again:\n%s", again)
	}
	mustCLI(t, address, "room", "#work", "--goal", "ship the lexer", "--token-file", token)
	rows := jsonLines[map[string]any](t, check("worker", "--json"))
	if rows[0]["type"] != "charter" || rows[0]["orchestrator"] != "lead" || rows[0]["goal"] != "ship the lexer" {
		t.Fatalf("a changed goal shows again: %v", rows)
	}

	// Only lead hands work out in #work; a done there says what it delivered.
	deniedCLI(t, address, "only lead, who orchestrates #work, may hand work out here", "handoff", "--nick", "worker", "--channel", "#work", "--message", "do it yourself")
	offer := cliJSON[map[string]any](t, address, "handoff", "--nick", "lead", "--channel", "#work", "--to", "worker", "--message", "port the lexer")
	id, _ := offer["id"].(string)
	mustCLI(t, address, "handoff", "accept", id, "--nick", "worker")
	deniedCLI(t, address, "say what was delivered: --message or --link", "handoff", "done", id, "--nick", "worker")
	mustCLI(t, address, "handoff", "done", id, "--nick", "worker", "--link", "https://example.com/pull/7")
	mustCLI(t, address, "handoff", "--nick", "worker", "--to", "lead", "--message", "a direct handoff stays free")

	if digest := mustCLI(t, address, "digest", "--nick", "worker", "--channel", "#work"); !strings.Contains(digest, "Goal of #work (lead orchestrates): ship the lexer\n") {
		t.Fatalf("worker's digest:\n%s", digest)
	}
	if digest := mustCLI(t, address, "digest", "--nick", "lead", "--channel", "#work"); !strings.Contains(digest, "Orchestrating #work: ship the lexer; 0 open · 0 in progress · 0 blocked · 1 done this week\n") {
		t.Fatalf("lead's digest:\n%s", digest)
	}
	digest := cliJSON[digestSummary](t, address, "digest", "--nick", "worker", "--channel", "#work")
	if len(digest.Goals) != 1 || digest.Goals[0].Orchestrator != "lead" || digest.Goals[0].Orchestrating {
		t.Fatalf("worker's digest goals: %+v", digest.Goals)
	}
	for _, w := range digest.Waiting {
		if strings.Contains(w.Preview, "orchestrates") {
			t.Fatalf("an announcement waits on the worker: %+v", digest.Waiting)
		}
	}

	// Off: nothing is left to show, and anyone hands work out again.
	mustCLI(t, address, "room", "#work", "--orchestrate", "off", "--token-file", token)
	if out := check("worker"); strings.Contains(out, "orchestrated by") || !strings.Contains(out, "@agents #work is no longer orchestrated.") {
		t.Fatalf("after off:\n%s", out)
	}
	mustCLI(t, address, "handoff", "--nick", "worker", "--channel", "#work", "--message", "anyone may now")
}

// The orchestrate tool runs room --orchestrate with the adapter's default
// admin credential: on, a goal change, off, and a call that names nothing
// refused before it reaches the daemon.
func TestMCPOrchestrateTool(t *testing.T) {
	address, token := chatServerWithToken(t, 64)
	t.Setenv("AIRC_ADMIN_TOKEN_FILE", token)
	mustCLI(t, address, "user", "create", "--nick", "lead", "--agent")
	binary := nativeCLIBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "acceptance", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: exec.Command(binary, "mcp", "--addr", address, "--nick", "boss")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	call := func(args map[string]any) (irc.ChatEntry, string) {
		t.Helper()
		r, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "orchestrate", Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(r.StructuredContent)
		var out struct{ Rows []irc.ChatEntry }
		if err := json.Unmarshal(data, &out); err != nil || r.IsError || len(out.Rows) != 1 {
			content, _ := json.Marshal(r.Content)
			return irc.ChatEntry{}, string(content)
		}
		return out.Rows[0], ""
	}
	if row, failed := call(map[string]any{"channel": "#work", "orchestrator": "lead", "goal": "ship the parser fix"}); failed != "" || row.Orchestrator != "lead" || row.Goal != "ship the parser fix" {
		t.Fatalf("on: %+v %s", row, failed)
	}
	if row, failed := call(map[string]any{"channel": "#work", "goal": "ship the lexer"}); failed != "" || row.Orchestrator != "lead" || row.Goal != "ship the lexer" {
		t.Fatalf("goal: %+v %s", row, failed)
	}
	if _, failed := call(map[string]any{"channel": "#work"}); !strings.Contains(failed, "name the orchestrator") {
		t.Fatalf("an empty call: %s", failed)
	}
	if _, failed := call(map[string]any{"channel": "#work", "orchestrator": "ghost"}); !strings.Contains(failed, "the orchestrator must be a registered nickname") {
		t.Fatalf("an unregistered orchestrator: %s", failed)
	}
	if row, failed := call(map[string]any{"channel": "#work", "off": true}); failed != "" || row.Orchestrator != "" || row.Goal != "" {
		t.Fatalf("off: %+v %s", row, failed)
	}
}
