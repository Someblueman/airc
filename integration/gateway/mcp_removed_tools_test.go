//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"testing"
)

func TestMCPHTTPRejectsRemovedOperationTools(t *testing.T) {
	f := newMCPHTTPFixture(t)
	for _, operation := range append(dispatch.ImplementedOperations(), core.CapabilitiesGet) {
		if operation == core.Send {
			continue
		}
		result, err := f.client.CallTool(t.Context(), &mcp.CallToolParams{Name: string(operation), Arguments: json.RawMessage(`{"request_id":"removed"}`)})
		if err == nil && (result == nil || !result.IsError) {
			t.Fatalf("removed tool %s remained callable: %+v", operation, result)
		}
	}
	status := mcpCallRequest(t, f.client, core.Request{Version: 1, RequestID: "removed-status", Operation: core.ReceiptGet, LookupID: "removed"})
	if !status.IsError {
		t.Fatal("removed tool created durable work", status.StructuredContent)
	}
}
