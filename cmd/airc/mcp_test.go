package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPRealStdioToolsAndCancellation(t *testing.T) {
	address := chatServer(t, 64)
	binary := filepath.Join(t.TempDir(), "airc")
	build := exec.Command("go", "build", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s %v", out, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "acceptance", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: exec.Command(binary, "mcp", "--addr", address, "--nick", "adapter")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 14 {
		t.Fatal(tools)
	}
	call := func(name string, args any) *mcp.CallToolResult {
		t.Helper()
		r, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || r.IsError {
			t.Fatalf("%s: %+v %v", name, r, err)
		}
		return r
	}
	sent := call("send", map[string]any{"channel": "#room", "message": "from MCP"})
	data, _ := json.Marshal(sent.StructuredContent)
	var output struct {
		Rows []struct {
			ID        string `json:"id"`
			RequestID string `json:"request_id"`
		}
	}
	if err := json.Unmarshal(data, &output); err != nil || len(output.Rows) != 1 || output.Rows[0].ID == "" {
		t.Fatal(string(data), err)
	}
	id := output.Rows[0].ID
	call("send", map[string]any{"retry": output.Rows[0].RequestID})
	call("send", map[string]any{"pending": true})
	call("thread", map[string]any{"id": id})
	contextResult := call("context", map[string]any{"id": id})
	data, _ = json.Marshal(contextResult.StructuredContent)
	if !strings.Contains(string(data), "from MCP") {
		t.Fatal(string(data))
	}
	call("directory", map[string]any{"who": "adapter"})
	call("prepare", map[string]any{"id": id, "seconds": 60, "message": "reviewing"})
	waitingSignal := call("waiting", map[string]any{"id": id})
	encoded, _ := json.Marshal(waitingSignal.StructuredContent)
	if !strings.Contains(string(encoded), "reviewing") {
		t.Fatal("reply signal missing", string(encoded))
	}
	call("cancel", map[string]any{"id": id})
	call("follow", map[string]any{"id": id})
	call("unfollow", map[string]any{"id": id})
	call("react", map[string]any{"id": id, "reaction": "agree"})
	call("correct", map[string]any{"id": id, "message": "corrected from MCP"})
	searchResult := call("search", map[string]any{"query": "MCP", "target": "#room", "limit": 1})
	encoded, _ = json.Marshal(searchResult.StructuredContent)
	if !strings.Contains(string(encoded), "cursor") || !strings.Contains(string(encoded), "more") {
		t.Fatal("search pagination missing", string(encoded))
	}
	call("retract", map[string]any{"id": id, "message": "withdrawn"})

	call("check", map[string]any{"channels": []string{"#room"}})
	waitCtx, stop := context.WithCancel(ctx)
	waiting := make(chan error, 1)
	go func() {
		_, err := session.CallTool(waitCtx, &mcp.CallToolParams{Name: "check", Arguments: map[string]any{"channels": []string{"#room"}, "wait_seconds": 10}})
		waiting <- err
	}()
	time.Sleep(100 * time.Millisecond)
	call("check", map[string]any{"reply_to": id, "include_own": true})
	secondCtx, secondStop := context.WithCancel(ctx)
	defer secondStop()
	secondWaiting := make(chan error, 1)
	go func() {
		_, err := session.CallTool(secondCtx, &mcp.CallToolParams{Name: "check", Arguments: map[string]any{"reply_to": id, "wait_seconds": 10}})
		secondWaiting <- err
	}()
	time.Sleep(100 * time.Millisecond)
	busy, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "check", Arguments: map[string]any{"reply_to": id, "wait_seconds": 10}})
	if err != nil || !busy.IsError {
		t.Fatalf("third wait was not rejected: %+v %v", busy, err)
	}
	call("send", map[string]any{"channel": "#elsewhere", "message": "send while two checks wait"})
	call("context", map[string]any{"id": id, "max_bytes": 2048})
	time.Sleep(100 * time.Millisecond)
	// Idle waits must allow another request using the same cursor namespace.
	call("check", map[string]any{"channels": []string{"#room"}})
	stop()
	select {
	case err := <-waiting:
		if err == nil {
			t.Fatal("cancelled wait returned success")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not return")
	}
	secondStop()
	select {
	case err := <-secondWaiting:
		if err == nil {
			t.Fatal("second cancelled wait returned success")
		}
	case <-time.After(time.Second):
		t.Fatal("second cancellation did not return")
	}
	time.Sleep(100 * time.Millisecond)
	call("check", map[string]any{"channels": []string{"#room"}})
	bad, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "send", Arguments: map[string]any{"message": "bad", "channel": "#room", "to": "other"}})
	if err != nil || !bad.IsError {
		t.Fatalf("invalid targets not rejected: %+v %v", bad, err)
	}
	// Exercise actual stdio EOF, independently of the client SDK's graceful
	// Close (which waits for its outstanding calls before closing the pipe).
	process := exec.Command(binary, "mcp", "--addr", address, "--nick", "adapter")
	input, err := process.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	outputPipe, err := process.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	defer process.Process.Kill()
	fmt.Fprintln(input, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"EOF-test","version":"1"}}}`)
	scanner := bufio.NewScanner(outputPipe)
	if !scanner.Scan() {
		t.Fatal("initialize response missing", scanner.Err())
	}
	fmt.Fprintln(input, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	fmt.Fprintln(input, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"check","arguments":{"channels":["#room"],"wait_seconds":10}}}`)
	time.Sleep(100 * time.Millisecond)
	input.Close()
	exited := make(chan error, 1)
	go func() { exited <- process.Wait() }()
	select {
	case err := <-exited:
		if err != nil {
			t.Fatal("EOF shutdown", err)
		}
	case <-time.After(time.Second):
		t.Fatal("stdio EOF did not cancel active calls")
	}
}

func TestMCPInputBoundsEachLine(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		wantError  bool
	}{
		{"at limit", strings.Repeat("x", 1<<20) + "\n", false},
		{"two bounded lines", strings.Repeat("x", 1<<20) + "\n" + strings.Repeat("y", 1<<20) + "\n", false},
		{"over limit", strings.Repeat("x", (1<<20)+1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := io.NopCloser(strings.NewReader(tc.text))
			reader := &mcpInputReader{source: source, reader: bufio.NewReader(source)}
			_, err := io.Copy(io.Discard, reader)
			if (err != nil) != tc.wantError {
				t.Fatalf("bounded input error: %v", err)
			}
		})
	}
}
