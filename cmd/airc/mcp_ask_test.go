package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The ask tool posts and waits in one call, returning the receipt, the
// reply row and the status; a bad target is refused.
func TestMCPAskPostsAndWaitsForTheReply(t *testing.T) {
	tap := tapDaemon(t, chatServer(t, 64))
	address := tap.addr
	binary := nativeCLIBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "acceptance", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: exec.Command(binary, "mcp", "--addr", address, "--nick", "asker")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if r, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "ask", Arguments: map[string]any{"message": "no target"}}); err == nil && !r.IsError {
		t.Fatalf("ask without a target was accepted: %+v", r)
	}
	type outcome struct {
		result *mcp.CallToolResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		r, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "ask", Arguments: map[string]any{"channel": "#room", "message": "Which branch?", "wait_seconds": 10, "run": "run-3"}})
		done <- outcome{r, err}
	}()
	tap.waitObserving(t, 1)
	question := historyIDs(t, mustCLI(t, address, "history", "#room", "--json"))
	if len(question) != 1 {
		t.Fatalf("the question was not posted: %v", question)
	}
	reply := replyMessage(t, address, "builder", question[0], "main")
	select {
	case got := <-done:
		if got.err != nil || got.result.IsError {
			t.Fatalf("ask: %+v %v", got.result, got.err)
		}
		data, _ := json.Marshal(got.result.StructuredContent)
		var output struct {
			Rows []struct {
				Type    string `json:"type"`
				ID      string `json:"id"`
				Code    string `json:"code"`
				Run     string `json:"run"`
				ReplyTo string `json:"reply_to"`
			}
		}
		if err := json.Unmarshal(data, &output); err != nil || len(output.Rows) != 3 {
			t.Fatalf("rows %s: %v", data, err)
		}
		if !strings.HasPrefix(output.Rows[0].Code, "accepted") || output.Rows[0].ID != question[0] || output.Rows[0].Run != "run-3" || output.Rows[1].ID != reply.ID || output.Rows[1].ReplyTo != question[0] || output.Rows[2].Type != "status" || output.Rows[2].Code != "messages" {
			t.Fatalf("rows %s", data)
		}
		if text := strings.TrimSpace(string(data)); !strings.Contains(text, "main") {
			t.Fatalf("the reply body is missing from %s", text)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("the reply did not end the ask")
	}
}
