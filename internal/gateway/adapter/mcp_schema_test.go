package adapter

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestMCPRequestSchemasExposeExactOperationFields(t *testing.T) {
	for _, op := range append([]core.Operation{}, core.Send, core.Reply, core.ContextRead, core.Watch, core.EventsWatch, core.Directory, core.ChannelsRead, core.MembersRead, core.ProfileUpdate, core.PollCreate, core.MessageRetract, core.CapabilitiesGet) {
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
	members := mcpRequestSchema(core.MembersRead)
	if _, ok := members["properties"].(map[string]any)["target"]; !ok {
		t.Fatal("members.read schema omits target")
	}
	if got := members["required"].([]string); len(got) != 4 || got[3] != "target" {
		t.Fatalf("members.read required fields = %#v", got)
	}
	live := mcpRequestSchema(core.EventsWatch)
	if _, ok := live["properties"].(map[string]any)["target"]; !ok {
		t.Fatal("events.watch schema omits target")
	}
}
