package matrixwire

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/Someblueman/airc/internal/gateway/jsoninput"
)

var ErrMessage = errors.New("invalid Matrix plain message")

// Message contains parsed data only. The owner must establish authenticated
// server provenance, membership, exact grants and application admission.
type Message struct {
	Room, Author, ID, Body string
	OriginMilliseconds     int64
}

// ParseMessage accepts one whole m.text event for an explicit room. Sync events
// may omit room_id; an included room must match exactly. Ancillary extension
// fields are bounded but never used as identity, request ID or freshness.
func ParseMessage(data []byte, room string) (Message, error) {
	if !RoomID(room) || jsoninput.Validate(data, 65536) != nil {
		return Message{}, ErrMessage
	}
	event, ok := object(data)
	if !ok {
		return Message{}, ErrMessage
	}
	if field(event, "type") != "m.room.message" {
		return Message{}, ErrMessage
	}
	for _, key := range []string{"state_key", "redacts"} {
		if _, exists := event[key]; exists {
			return Message{}, ErrMessage
		}
	}
	if _, exists := event["room_id"]; exists && field(event, "room_id") != room {
		return Message{}, ErrMessage
	}
	author, id := field(event, "sender"), field(event, "event_id")
	if !UserID(author) || !EventID(id) {
		return Message{}, ErrMessage
	}
	stamp, err := strconv.ParseInt(string(event["origin_server_ts"]), 10, 64)
	if err != nil || stamp < 0 || stamp > 9007199254740991 {
		return Message{}, ErrMessage
	}
	content, ok := object(event["content"])
	if !ok || field(content, "msgtype") != "m.text" {
		return Message{}, ErrMessage
	}
	for _, key := range []string{"m.relates_to", "m.new_content"} {
		if _, exists := content[key]; exists {
			return Message{}, ErrMessage
		}
	}
	body := field(content, "body")
	if strings.TrimSpace(body) == "" || strings.ContainsRune(body, 0) {
		return Message{}, ErrMessage
	}
	if raw, exists := event["unsigned"]; exists {
		unsigned, ok := object(raw)
		if !ok {
			return Message{}, ErrMessage
		}
		if _, redacted := unsigned["redacted_because"]; redacted {
			return Message{}, ErrMessage
		}
	}
	return Message{Room: room, Author: author, ID: id, Body: body, OriginMilliseconds: stamp}, nil
}

func object(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	var fields map[string]json.RawMessage
	err := json.Unmarshal(raw, &fields)
	return fields, err == nil && fields != nil
}
func field(fields map[string]json.RawMessage, key string) string {
	var value string
	if json.Unmarshal(fields[key], &value) != nil {
		return ""
	}
	return value
}
