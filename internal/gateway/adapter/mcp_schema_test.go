package adapter

import "testing"

func TestMCPRequestSchemasExposeOnlyToolFields(t *testing.T) {
	for _, name := range []string{"send", "read", "wait", "status"} {
		schema := mcpRequestSchema(name)
		if schema["type"] != "object" || schema["additionalProperties"] != false {
			t.Fatal("non-strict schema", name, schema)
		}
		properties := schema["properties"].(map[string]any)
		for _, removed := range []string{"version", "operation", "query", "profile", "poll", "reaction"} {
			if _, ok := properties[removed]; ok {
				t.Fatal("removed field exposed", name, removed)
			}
		}
		if _, ok := properties["request_id"]; !ok {
			t.Fatal("missing stable ID", name)
		}
	}
	send := mcpRequestSchema("send")["properties"].(map[string]any)
	if _, ok := send["parent_id"]; ok {
		t.Fatal("send exposes reply selector")
	}
	if _, ok := send["message"]; !ok {
		t.Fatal("send omits message")
	}
	read := mcpRequestSchema("read")["properties"].(map[string]any)
	target := read["target"].(map[string]any)["properties"].(map[string]any)
	if target["kind"].(map[string]any)["const"] != "room" {
		t.Fatal("read admits direct targets")
	}
	status := mcpRequestSchema("status")
	if len(status["required"].([]string)) != 1 || len(status["properties"].(map[string]any)) != 2 {
		t.Fatal("status schema is not narrow", status)
	}
}
