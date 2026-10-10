//go:build darwin || linux

package adapter

import (
	"encoding/json"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"slices"
	"strings"
	"testing"
)

func TestMCPPublicSDKScopesToolsAndTargetsToPrincipal(t *testing.T) {
	f := mcpLifecycle(t, core.Send, core.ReceiptGet)
	session, err := f.connect(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	tools, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	if !slices.Equal(names, []string{"send", "status"}) {
		t.Fatal("unscoped tools", names)
	}
	call, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "status", Arguments: json.RawMessage(`{"request_id":"scope"}`)})
	if err != nil || call.IsError {
		t.Fatal(call, err)
	}
	body, err := json.Marshal(call.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var discovery mcpDiscovery
	if err := json.Unmarshal(body, &discovery); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(discovery.Tools, []string{"send", "status"}) || !discovery.ReceiptLookup || len(discovery.Targets) != 1 || !slices.Equal(discovery.Targets["send"], []core.Target{{Kind: "room", Name: "#owner"}, {Kind: "direct", Name: "owner"}}) {
		t.Fatalf("unscoped discovery=%+v", discovery)
	}
	for _, secret := range []string{f.tokens[0], f.tokens[1], "upstream", "#other", "binding", "operations"} {
		if strings.Contains(string(body), secret) {
			t.Fatal("discovery leaked unavailable facts", secret, string(body))
		}
	}
	forbidden, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "send", Arguments: json.RawMessage(`{"request_id":"forbidden","target":{"kind":"room","name":"#other"},"message":"body"}`)})
	if err != nil || !forbidden.IsError {
		t.Fatal("cross-principal target admitted", forbidden, err)
	}
}

func TestMCPStatusWithoutReceiptPermissionCannotLookUpJobs(t *testing.T) {
	f := mcpLifecycle(t)
	session, err := f.connect(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	call, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "status", Arguments: json.RawMessage(`{"request_id":"denied","lookup_id":"original"}`)})
	if err != nil || !call.IsError {
		t.Fatal("receipt permission bypassed", call, err)
	}
	var failure core.Failure
	body, _ := json.Marshal(call.StructuredContent)
	if err := json.Unmarshal(body, &failure); err != nil || failure.Code != core.Forbidden {
		t.Fatal("receipt denial", string(body), err)
	}
}
