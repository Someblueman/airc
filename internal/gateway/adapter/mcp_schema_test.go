package adapter

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestMCPRequestSchemasExposeExactOperationFields(t *testing.T) {
	for _, op := range append([]core.Operation{}, core.Send, core.Reply, core.ContextRead, core.Watch, core.Directory, core.ProfileUpdate, core.PollCreate, core.MessageRetract, core.CapabilitiesGet) {
		schema := mcpRequestSchema(op)
		if schema["type"] != "object" || schema["additionalProperties"] != false {
			t.Fatalf("%s missing strict object schema: %#v", op, schema)
		}
		properties := schema["properties"].(map[string]any)
		if properties["operation"].(map[string]any)["const"] != string(op) {
			t.Fatalf("%s missing operation const", op)
		}
	}
	if _, ok := mcpRequestSchema(core.Send)["properties"].(map[string]any)["parent_id"]; ok {
		t.Fatal("send schema exposes parent_id")
	}
	if _, ok := mcpRequestSchema(core.Reply)["properties"].(map[string]any)["parent_id"]; !ok {
		t.Fatal("reply schema omits parent_id")
	}
}
