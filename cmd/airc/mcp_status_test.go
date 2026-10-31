package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The status tool posts a shaped status and the resolve tool closes a
// question; both refuse what the daemon would, before connecting.
func TestMCPStatusAndResolve(t *testing.T) {
	address := chatServer(t, 64)
	binary := nativeCLIBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "acceptance", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: exec.Command(binary, "mcp", "--addr", address, "--nick", "builder")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	call := func(name string, args any) *mcp.CallToolResult {
		t.Helper()
		r, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || r.IsError {
			t.Fatalf("%s: %+v %v", name, r, err)
		}
		return r
	}
	refused := func(name, want string, args any) {
		t.Helper()
		r, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err == nil && !r.IsError {
			t.Fatalf("%s %v was accepted: %+v", name, args, r)
		}
		text := fmt.Sprint(err)
		if r != nil {
			data, _ := json.Marshal(r.Content)
			text += string(data)
		}
		if !strings.Contains(text, want) {
			t.Fatalf("%s %v: want %q in %s", name, args, want, text)
		}
	}
	rows := func(r *mcp.CallToolResult, out any) {
		t.Helper()
		data, _ := json.Marshal(r.StructuredContent)
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatal(string(data), err)
		}
	}

	refused("status", "working, blocked or done", map[string]any{"channel": "#room", "task": "port the fix", "state": "idle"})
	refused("status", "go with --task", map[string]any{"channel": "#room", "state": "working"})
	var listed struct{ Rows []statusRow }
	rows(call("status", map[string]any{"channel": "#room"}), &listed)
	if len(listed.Rows) != 0 {
		t.Fatalf("statuses before any post %+v", listed.Rows)
	}
	refused("status", "absolute http(s) URL", map[string]any{"channel": "#room", "task": "port the fix", "state": "done", "link": "pr/54"})
	var posted struct{ Rows []statusRow }
	rows(call("status", map[string]any{"channel": "#room", "task": "port the fix", "state": "working", "message": "on branch work-7"}), &posted)
	if len(posted.Rows) != 1 || posted.Rows[0].Type != "status" || posted.Rows[0].Nick != "builder" || posted.Rows[0].State != "working" || posted.Rows[0].Message != "Status [working] port the fix: on branch work-7" {
		t.Fatalf("status rows %+v", posted.Rows)
	}
	link := "https://github.com/o/r/pull/54"
	rows(call("status", map[string]any{"channel": "#room", "task": "port the fix", "state": "done", "link": link}), &posted)
	if len(posted.Rows) != 1 || posted.Rows[0].State != "done" || posted.Rows[0].Link != link {
		t.Fatalf("done rows %+v", posted.Rows)
	}
	if listed := jsonLines[statusRow](t, mustCLI(t, address, "status", "--channel", "room", "--json")); len(listed) != 1 || listed[0].State != "done" || listed[0].Link != link {
		t.Fatalf("the room's statuses %+v", listed)
	}
	rows(call("status", map[string]any{"channel": "#room"}), &listed)
	if len(listed.Rows) != 1 || listed.Rows[0].Nick != "builder" || listed.Rows[0].State != "done" || listed.Rows[0].Link != link {
		t.Fatalf("the room's statuses over MCP %+v", listed.Rows)
	}
	var directory struct {
		Rows []struct {
			Nick   string `json:"nick"`
			Status *struct {
				State string `json:"state"`
				Task  string `json:"task"`
			} `json:"status"`
		}
	}
	rows(call("directory", map[string]any{"who": "builder"}), &directory)
	if len(directory.Rows) != 1 || directory.Rows[0].Status == nil || directory.Rows[0].Status.State != "done" || directory.Rows[0].Status.Task != "port the fix" {
		t.Fatalf("directory rows %+v", directory.Rows)
	}

	question := send(t, address, "planner", "room", "builder: can you review it today?")
	refused("resolve", "valid message ID", map[string]any{"id": "nope"})
	var resolved struct{ Rows []resolveRow }
	// An abbreviated ID resolves on the daemon; the row carries the full one.
	rows(call("resolve", map[string]any{"id": question.ID[:8], "message": "settled in the DM"}), &resolved)
	if len(resolved.Rows) != 1 || resolved.Rows[0].Type != "resolved" || resolved.Rows[0].ID != question.ID || resolved.Rows[0].From != "builder" || resolved.Rows[0].Message != "settled in the DM" {
		t.Fatalf("resolve rows %+v", resolved.Rows)
	}
	call("react", map[string]any{"id": question.ID[:8], "reaction": "seen"})
	call("follow", map[string]any{"id": question.ID[:10]})
	call("follow", map[string]any{"step": "unfollow", "id": question.ID[:10]})
	refused("react", "valid message ID", map[string]any{"id": question.ID[:7], "reaction": "seen"})
	var digest struct {
		Rows []digestSummary
	}
	rows(call("digest", map[string]any{"channels": []string{"#room"}}), &digest)
	if len(digest.Rows) != 1 || len(digest.Rows[0].Waiting) != 0 || len(digest.Rows[0].Rooms) != 1 || len(digest.Rows[0].Rooms[0].Statuses) != 1 || digest.Rows[0].Rooms[0].Statuses[0].State != "done" {
		t.Fatalf("digest rows %+v", digest.Rows)
	}
}
