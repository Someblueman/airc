package adapter

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/mcpserver"
)

func TestDecodeMCPRequestUsesStrictToolJSON(t *testing.T) {
	valid := `{"request_id":"mcp-1","target":{"kind":"room","name":"#room"},"message":"hello"}`
	request, err := decodeMCPRequest("send", []byte(valid))
	if err != nil || request.Version != 1 || request.Operation != core.Send || request.RequestID != "mcp-1" {
		t.Fatal(request, err)
	}
	for name, body := range map[string]string{
		"duplicate": `{"request_id":"one","request_id":"two","target":{"kind":"room","name":"#room"},"message":"hello"}`,
		"case":      `{"request_id":"case","Target":{"kind":"room","name":"#room"},"message":"hello"}`,
		"null":      `{"request_id":"null","target":null,"message":"hello"}`,
		"operation": `{"request_id":"op","operation":"send","target":{"kind":"room","name":"#room"},"message":"hello"}`,
		"version":   `{"version":1,"request_id":"version","target":{"kind":"room","name":"#room"},"message":"hello"}`,
		"unrelated": `{"request_id":"extra","target":{"kind":"room","name":"#room"},"message":"hello","parent_id":"0123456789abcdef0123456789abcdef"}`,
		"oversized": strings.Repeat("x", core.MaxRequestBytes+1),
	} {
		if _, err := decodeMCPRequest("send", []byte(body)); err == nil {
			t.Fatal("accepted", name)
		}
	}
	for _, name := range []string{"read", "wait"} {
		request, err := decodeMCPRequest(name, []byte(`{"request_id":"page","target":{"kind":"room","name":"#room"},"limit":2}`))
		if err != nil || request.Operation != core.HistoryRead && request.Operation != core.Watch {
			t.Fatal(request, err)
		}
		if _, err := decodeMCPRequest(name, []byte(`{"request_id":"direct","target":{"kind":"direct","name":"other"},"limit":2}`)); err == nil {
			t.Fatal("direct read admitted", name)
		}
	}
	for _, body := range []string{`{"request_id":"s","lookup_id":""}`, `{"request_id":"s","lookup_id":null}`, `{"request_id":"s","target":{"kind":"room","name":"#room"}}`} {
		if _, err := decodeMCPRequest("status", []byte(body)); err == nil {
			t.Fatal("invalid status admitted", body)
		}
	}
	request, err = decodeMCPRequest("status", []byte(`{"request_id":"s"}`))
	if err != nil || request.Operation != core.CapabilitiesGet {
		t.Fatal(request, err)
	}
	request, err = decodeMCPRequest("status", []byte(`{"request_id":"s","lookup_id":"original"}`))
	if err != nil || request.Operation != core.ReceiptGet || request.LookupID != "original" {
		t.Fatal(request, err)
	}
	if _, err := decodeMCPRequest("history.read", []byte(valid)); err == nil {
		t.Fatal("removed name decoded")
	}
}

func TestMCPToolResultAndFailureAreBounded(t *testing.T) {
	if _, err := mcpToolResult(core.Send, map[string]string{"ok": "yes"}); err != nil {
		t.Fatal(err)
	}
	if result, err := mcpToolError(core.InvalidRequest); err != nil || !result.IsError {
		t.Fatalf("failure result=%#v err=%v", result, err)
	}
	result, err := mcpToolResult(core.Send, map[string]string{"escaped": strings.Repeat("\"", maxMCPToolBytes)})
	if err != nil || !result.IsError {
		t.Fatalf("oversized structured result accepted: %+v %v", result, err)
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > maxMCPToolBytes {
		t.Fatal("capacity error itself exceeded result budget", len(encoded), err)
	}
}

func TestMCPWatchRequiresProgressAndBounds(t *testing.T) {
	if maxMCPStreamFrames != MaxStreamFrames || maxMCPStreamBytes != MaxStreamBytes || maxMCPToolBytes >= MaxResponseFrameBytes {
		t.Fatal("MCP stream bounds are not tied to framed adapter budgets")
	}
	for _, token := range []any{"", strings.Repeat("x", 65), float64(-1), 1.5} {
		if validMCPProgressToken(token) {
			t.Fatalf("accepted invalid progress token %#v", token)
		}
	}
	for _, token := range []any{"p", float64(0), float64(12)} {
		if !validMCPProgressToken(token) {
			t.Fatalf("rejected valid progress token %#v", token)
		}
	}
}

func TestMCPWatchProgressTokenModes(t *testing.T) {
	if token := (&mcpserver.Call{}).ProgressToken(); token != nil {
		t.Fatalf("absent progress token = %#v", token)
	}
	for _, token := range []any{"", strings.Repeat("x", 65), float64(-1), 1.5} {
		if validMCPProgressToken(token) {
			t.Fatalf("accepted invalid present progress token %#v", token)
		}
	}
}
