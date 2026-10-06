//go:build darwin || linux

package adapter

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
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
	if len(tools.Tools) != 1 {
		t.Fatalf("installed tools=%d", len(tools.Tools))
	}
	for _, tool := range tools.Tools {
		if tool.Name != "status" {
			continue
		}
		encoded, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema struct {
			Required []string `json:"required"`
		}
		if err := json.Unmarshal(encoded, &schema); err != nil || !slices.Contains(schema.Required, "request_id") {
			t.Fatal("SDK discovery omitted required status argument", schema, err)
		}
	}
	for _, body := range []string{
		`{"request_id":"first","request_id":"second","target":{"kind":"room","name":"#room"},"message":"body"}`,
		`{"request_id":"null","target":null,"message":"body"}`,
		`{"request_id":"case","Target":{"kind":"room","name":"#room"},"message":"body"}`,
		`{"version":1,"request_id":"mismatch","operation":"receipt.get","lookup_id":"other"}`,
		`{"request_id":"size","target":{"kind":"room","name":"#room"},"message":"` + strings.Repeat("x", 4097) + `"}`,
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
	direct, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "read", Arguments: json.RawMessage(`{"request_id":"direct","target":{"kind":"direct","name":"other"},"limit":2}`)})
	if err != nil {
		t.Fatal(err)
	}
	var failure core.Failure
	body, _ := json.Marshal(direct.StructuredContent)
	if err := json.Unmarshal(body, &failure); err != nil || !direct.IsError || failure.Code != core.InvalidRequest {
		t.Fatalf("room-only read boundary lost: %+v %v", direct, err)
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "status", Arguments: json.RawMessage(`{"request_id":"caps"}`)})
	if err != nil || result.IsError {
		t.Fatalf("valid discovery rejected: result=%+v error=%v", result, err)
	}
}
