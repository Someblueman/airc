package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The paste tool stores text and, with a target, posts the message that
// shares it; paste_get reads it back whole; without a target nothing is
// posted.
func TestMCPPasteAndPasteGet(t *testing.T) {
	address := chatServer(t, 64)
	binary := nativeCLIBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "acceptance", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: exec.Command(binary, "mcp", "--addr", address, "--nick", "builder")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	rows := func(name string, args any) []map[string]any {
		t.Helper()
		r, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || r.IsError {
			t.Fatalf("%s: %+v %v", name, r, err)
		}
		data, _ := json.Marshal(r.StructuredContent)
		var out struct{ Rows []map[string]any }
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatal(string(data), err)
		}
		return out.Rows
	}
	content := strings.Repeat("FAIL: TestThing (0.00s)\n    thing_test.go:12: want 1, got 2\n", 200)
	id := protocol.PasteID([]byte(content))
	shared := rows("paste", map[string]any{"content": content, "name": "go-test.log", "channel": "#room", "message": "the failing run"})
	if len(shared) != 1 {
		t.Fatalf("paste rows %+v", shared)
	}
	paste, _ := shared[0]["paste"].(map[string]any)
	if paste["id"] != id || paste["name"] != "go-test.log" || paste["language"] != "text" || !strings.HasPrefix(shared[0]["message"].(string), "the failing run\npaste:"+id) {
		t.Fatalf("shared %+v", shared[0])
	}
	got := rows("paste_get", map[string]any{"id": id})
	if len(got) != 1 || got[0]["content"] != content || got[0]["from"] != "builder" || got[0]["lines"] != float64(400) {
		t.Fatalf("paste_get %+v", got)
	}
	stored := rows("paste", map[string]any{"content": "# notes\n", "name": "notes.md"})
	if len(stored) != 1 || stored[0]["type"] != "paste" || stored[0]["id"] != protocol.PasteID([]byte("# notes\n")) || stored[0]["language"] != "text" {
		t.Fatalf("stored %+v", stored)
	}
	if r, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "paste", Arguments: map[string]any{"content": "x", "name": "x", "message": "caption without a target"}}); err == nil && !r.IsError {
		t.Fatalf("a caption without a target was accepted: %+v", r)
	}
	if r, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "paste_get", Arguments: map[string]any{"id": "nope"}}); err == nil && !r.IsError {
		t.Fatalf("a bad id was accepted: %+v", r)
	}
	seen := checkMessages(t, mustCLI(t, address, "check", "--nick", "reviewer", "--channel", "room", "--json"))
	if len(seen) != 1 || seen[0].Paste == nil || seen[0].Paste.ID != id {
		t.Fatalf("the room saw %+v", seen)
	}
}
