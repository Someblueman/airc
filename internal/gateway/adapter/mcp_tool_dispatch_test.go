//go:build darwin || linux

package adapter

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPPublicSDKPreservesStrictToolArguments(t *testing.T) {
	f := mcpLifecycle(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	session, err := f.connect(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 31 {
		t.Fatalf("installed tools=%d", len(tools.Tools))
	}
	for _, tool := range tools.Tools {
		if tool.Name != "send" {
			continue
		}
		encoded, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema struct {
			Required []string `json:"required"`
		}
		if err := json.Unmarshal(encoded, &schema); err != nil || !slices.Contains(schema.Required, "target") || !slices.Contains(schema.Required, "message") {
			t.Fatal("SDK discovery omitted required send arguments", schema, err)
		}
	}
	for _, body := range []string{
		`{"version":1,"request_id":"first","request_id":"second","operation":"send","target":{"kind":"room","name":"#room"},"message":"body"}`,
		`{"version":1,"request_id":"null","operation":"send","target":null,"message":"body"}`,
		`{"version":1,"request_id":"case","operation":"send","Target":{"kind":"room","name":"#room"},"message":"body"}`,
		`{"version":1,"request_id":"mismatch","operation":"receipt.get","lookup_id":"other"}`,
	} {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "send", Arguments: json.RawMessage(body)})
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		var failure core.Failure
		if err := json.Unmarshal(encoded, &failure); err != nil || !result.IsError || failure.Code != core.InvalidRequest {
			t.Fatalf("strict arguments lost: result=%+v error=%v", result, err)
		}
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "capabilities.get", Arguments: json.RawMessage(`{"version":1,"request_id":"caps","operation":"capabilities.get"}`)})
	if err != nil || result.IsError {
		t.Fatalf("valid discovery rejected: result=%+v error=%v", result, err)
	}
}
