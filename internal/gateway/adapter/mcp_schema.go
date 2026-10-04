package adapter

import "github.com/Someblueman/airc/internal/gateway/core"

func mcpRequestSchema(operation core.Operation) map[string]any {
	properties := map[string]any{
		"version":         map[string]any{"type": "integer", "const": 1},
		"request_id":      map[string]any{"type": "string", "minLength": 1, "maxLength": 64, "pattern": `^[A-Za-z0-9_-]+$`},
		"operation":       map[string]any{"type": "string", "const": string(operation)},
		"target":          mcpTargetSchema(),
		"message":         map[string]any{"type": "string", "maxLength": 4096},
		"reaction":        map[string]any{"type": "string", "maxLength": 32},
		"parent_id":       mcpIDSchema(),
		"poll_id":         mcpIDSchema(),
		"thread_mode":     map[string]any{"type": "string", "enum": []string{"thread", "replies"}},
		"lookup_id":       map[string]any{"type": "string", "minLength": 1, "maxLength": 64, "pattern": `^[A-Za-z0-9_-]+$`},
		"cursor":          map[string]any{"type": "string", "maxLength": 2048},
		"start":           map[string]any{"type": "string", "enum": []string{"recent", "retained"}},
		"query":           map[string]any{"type": "string", "maxLength": 256},
		"from":            map[string]any{"type": "string", "maxLength": 30},
		"limit":           map[string]any{"type": "integer", "minimum": 0, "maximum": 1000},
		"max_bytes":       map[string]any{"type": "integer", "minimum": 0, "maximum": 65536},
		"issued_at":       map[string]any{"type": "integer", "minimum": 0},
		"expires_at":      map[string]any{"type": "integer", "minimum": 0},
		"delivery_policy": map[string]any{"type": "string", "enum": []string{"retry", "receipt_only"}},
		"profile":         mcpProfileSchema(),
		"presence":        mcpPresenceSchema(),
		"topic":           map[string]any{"type": "string", "maxLength": 400},
		"room":            mcpRoomSchema(),
		"activity":        mcpActivitySchema(),
		"reply_signal":    mcpReplySignalSchema(),
		"poll":            mcpPollSchema(),
		"choice":          map[string]any{"type": "integer", "minimum": 1, "maximum": 8},
	}
	allowed := map[core.Operation][]string{
		core.Send:        {"target", "message", "issued_at", "expires_at", "delivery_policy"},
		core.Reply:       {"target", "message", "parent_id", "issued_at", "expires_at", "delivery_policy"},
		core.ReactionAdd: {"target", "parent_id", "reaction", "issued_at", "expires_at", "delivery_policy"},
		core.ContextRead: {"target", "parent_id", "limit", "max_bytes"},
		core.PinsRead:    {"target"}, core.RoomRead: {"target"},
		core.WaitingRead: {"target", "parent_id"}, core.PollResults: {"target", "poll_id"}, core.TopicRead: {"target"},
		core.ReceiptGet:    {"lookup_id"},
		core.HistoryRead:   {"target", "parent_id", "thread_mode", "cursor", "start", "limit"},
		core.Search:        {"target", "parent_id", "thread_mode", "cursor", "limit", "query", "from"},
		core.Watch:         {"target", "parent_id", "thread_mode", "cursor", "start", "limit"},
		core.Directory:     {"query", "limit"},
		core.ProfileUpdate: {"profile", "issued_at", "expires_at", "delivery_policy"},
		core.PresenceSet:   {"presence", "issued_at", "expires_at", "delivery_policy"},
		core.TopicSet:      {"target", "topic", "issued_at", "expires_at", "delivery_policy"},
		core.RoomUpdate:    {"target", "room", "issued_at", "expires_at", "delivery_policy"},
		core.PinsAdd:       {"target", "parent_id", "issued_at", "expires_at", "delivery_policy"}, core.PinsRemove: {"target", "parent_id", "issued_at", "expires_at", "delivery_policy"},
		core.ActivitySet:    {"target", "activity", "issued_at", "expires_at", "delivery_policy"},
		core.ReplySignalSet: {"target", "parent_id", "reply_signal", "issued_at", "expires_at", "delivery_policy"}, core.ReplySignalCancel: {"target", "parent_id", "issued_at", "expires_at", "delivery_policy"},
		core.PollCreate: {"target", "poll", "issued_at", "expires_at", "delivery_policy"}, core.PollVote: {"target", "poll_id", "choice", "issued_at", "expires_at", "delivery_policy"}, core.PollClose: {"target", "poll_id", "issued_at", "expires_at", "delivery_policy"},
		core.MessageAction: {"target", "message", "issued_at", "expires_at", "delivery_policy"}, core.MessageCorrect: {"target", "parent_id", "message", "issued_at", "expires_at", "delivery_policy"}, core.MessageRetract: {"target", "parent_id", "message", "issued_at", "expires_at", "delivery_policy"},
		core.CapabilitiesGet: {},
	}
	result := map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{}, "required": []string{"version", "request_id", "operation"}}
	selected := result["properties"].(map[string]any)
	selected["version"], selected["request_id"], selected["operation"] = properties["version"], properties["request_id"], properties["operation"]
	for _, name := range allowed[operation] {
		selected[name] = properties[name]
	}
	required := map[core.Operation][]string{
		core.Send: {"target", "message"}, core.Reply: {"target", "parent_id", "message"},
		core.ReactionAdd: {"target", "parent_id", "reaction"},
		core.ContextRead: {"target", "parent_id"}, core.PinsRead: {"target"}, core.RoomRead: {"target"},
		core.WaitingRead: {"target", "parent_id"}, core.PollResults: {"target", "poll_id"}, core.TopicRead: {"target"},
		core.ReceiptGet: {"lookup_id"}, core.HistoryRead: {"target", "limit"}, core.Search: {"target", "limit", "query"}, core.Watch: {"target", "limit"}, core.Directory: {"limit"},
		core.ProfileUpdate: {"profile"}, core.PresenceSet: {"presence"}, core.TopicSet: {"target", "topic"}, core.RoomUpdate: {"target", "room"},
		core.PinsAdd: {"target", "parent_id"}, core.PinsRemove: {"target", "parent_id"}, core.ActivitySet: {"target", "activity"},
		core.ReplySignalSet: {"target", "parent_id", "reply_signal"}, core.ReplySignalCancel: {"target", "parent_id"},
		core.PollCreate: {"target", "poll"}, core.PollVote: {"target", "poll_id", "choice"}, core.PollClose: {"target", "poll_id"},
		core.MessageAction: {"target", "message"}, core.MessageCorrect: {"target", "parent_id", "message"}, core.MessageRetract: {"target", "parent_id"},
	}
	result["required"] = append(result["required"].([]string), required[operation]...)
	if operation == core.Reply || operation == core.ReactionAdd || operation == core.ContextRead || operation == core.PinsAdd || operation == core.PinsRemove || operation == core.ReplySignalSet || operation == core.ReplySignalCancel {
		selected["parent_id"] = map[string]any{"type": "string", "pattern": `^[0-9a-fA-F]{32}$`}
	}
	if operation == core.HistoryRead || operation == core.Search || operation == core.Watch {
		selected["limit"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 1000}
	}
	if operation == core.Directory {
		selected["limit"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 256}
		selected["query"] = map[string]any{"type": "string", "maxLength": 30}
	}
	if operation == core.ContextRead {
		selected["max_bytes"] = map[string]any{"oneOf": []any{map[string]any{"const": 0}, map[string]any{"type": "integer", "minimum": core.MinContextBytes, "maximum": core.MaxContextBytes}}}
	}
	switch operation {
	case core.ReactionAdd, core.ContextRead, core.PinsRead, core.RoomRead, core.WaitingRead, core.PollResults, core.TopicRead, core.TopicSet, core.RoomUpdate, core.PinsAdd, core.PinsRemove, core.PollCreate, core.PollVote, core.PollClose:
		target := mcpTargetSchema()
		target["properties"].(map[string]any)["kind"] = map[string]any{"type": "string", "const": "room"}
		selected["target"] = target
	}
	return result
}

func mcpIDSchema() map[string]any {
	return map[string]any{"type": "string", "pattern": `^[0-9a-f]{32}$`}
}

func mcpTargetSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"kind": map[string]any{"type": "string", "enum": []string{"room", "direct"}}, "name": map[string]any{"type": "string", "minLength": 1, "maxLength": 64}}, "required": []string{"kind", "name"}}
}

func mcpProfileSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"model": map[string]any{"type": "string", "maxLength": 400}, "workspace": map[string]any{"type": "string", "maxLength": 400}, "tools": map[string]any{"type": "string", "maxLength": 400}, "about": map[string]any{"type": "string", "maxLength": 400}, "clear": map[string]any{"type": "boolean"}}}
}

func mcpPresenceSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"state": map[string]any{"type": "string", "enum": []string{"available", "thinking", "running", "away", "clear"}}, "note": map[string]any{"type": "string", "maxLength": 240}, "ttl_seconds": map[string]any{"type": "integer", "minimum": 0, "maximum": 3600}}, "required": []string{"state", "ttl_seconds"}}
}

func mcpRoomSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"slow_seconds": map[string]any{"type": "integer", "minimum": 0, "maximum": 3600}, "history_limit": map[string]any{"type": "integer", "minimum": 0}}, "minProperties": 1, "maxProperties": 2}
}

func mcpActivitySchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"kind": map[string]any{"type": "string", "enum": []string{"typing", "thinking"}}, "note": map[string]any{"type": "string", "maxLength": 240}, "ttl_seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 15}}, "required": []string{"kind", "ttl_seconds"}}
}

func mcpReplySignalSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"note": map[string]any{"type": "string", "maxLength": 240}, "ttl_seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 900}}, "required": []string{"ttl_seconds"}}
}

func mcpPollSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"question": map[string]any{"type": "string", "maxLength": 1000}, "options": map[string]any{"type": "array", "minItems": 2, "maxItems": 8, "items": map[string]any{"type": "string", "maxLength": 80}}, "ttl_seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 604800}}, "required": []string{"question", "options", "ttl_seconds"}}
}
