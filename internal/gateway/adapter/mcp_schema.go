package adapter

func mcpRequestSchema(name string) map[string]any {
	id := map[string]any{"type": "string", "minLength": 1, "maxLength": 64, "pattern": `^[A-Za-z0-9_-]+$`}
	properties := map[string]any{"request_id": id}
	required := []string{"request_id"}
	switch name {
	case "send":
		properties["target"] = mcpTargetSchema(false)
		properties["message"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 4096}
		properties["issued_at"] = map[string]any{"type": "integer", "minimum": 0}
		properties["expires_at"] = map[string]any{"type": "integer", "minimum": 0}
		properties["delivery_policy"] = map[string]any{"type": "string", "enum": []string{"retry", "receipt_only"}}
		required = append(required, "target", "message")
	case "read", "wait":
		properties["target"] = mcpTargetSchema(true)
		properties["limit"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 1000}
		properties["parent_id"] = map[string]any{"type": "string", "pattern": `^[0-9a-f]{32}$`}
		properties["thread_mode"] = map[string]any{"type": "string", "enum": []string{"thread", "replies"}}
		properties["cursor"] = map[string]any{"type": "string", "maxLength": 2048}
		properties["start"] = map[string]any{"type": "string", "pattern": `^(recent|retained|[0-9a-f]{32})$`}
		required = append(required, "target", "limit")
	case "status":
		properties["lookup_id"] = id
	}
	return map[string]any{"type": "object", "additionalProperties": false, "properties": properties, "required": required}
}

func mcpTargetSchema(roomOnly bool) map[string]any {
	kind := map[string]any{"type": "string", "enum": []string{"room", "direct"}}
	if roomOnly {
		kind = map[string]any{"type": "string", "const": "room"}
	}
	return map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"kind": kind, "name": map[string]any{"type": "string", "minLength": 1, "maxLength": 64}}, "required": []string{"kind", "name"}}
}
