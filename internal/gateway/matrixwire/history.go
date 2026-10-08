package matrixwire

import (
	"encoding/json"
	"errors"
	"github.com/Someblueman/airc/internal/gateway/jsoninput"
)

var ErrHistory = errors.New("invalid Matrix history page")

// History is parsed data, not an authenticated source or permission to advance
// intake. Only an absent end token means exhaustion; empty chunks may continue.
type History struct {
	Start, End string
	Exhausted  bool
	Events     []EventReference
}

// ParseHistory preserves server chunk order (forward or backward as requested).
// It bounds the entire response, including ancillary state, to 64 KiB/depth 16
// and the chunk to 256 events. References require separate verified retrieval.
func ParseHistory(data []byte, room string) (History, error) {
	if !RoomID(room) || jsoninput.ValidateDepth(data, 65536, 16) != nil {
		return History{}, ErrHistory
	}
	root, ok := object(data)
	if !ok || !Cursor(field(root, "start")) {
		return History{}, ErrHistory
	}
	page := History{Start: field(root, "start"), Exhausted: true}
	if _, exists := root["end"]; exists {
		page.End = field(root, "end")
		if !Cursor(page.End) || page.End == page.Start {
			return History{}, ErrHistory
		}
		page.Exhausted = false
	}
	var chunk []json.RawMessage
	if json.Unmarshal(root["chunk"], &chunk) != nil || chunk == nil || len(chunk) > 256 {
		return History{}, ErrHistory
	}
	seen := map[string]bool{}
	for _, raw := range chunk {
		event, ok := object(raw)
		id, kind := field(event, "event_id"), field(event, "type")
		if !ok || !EventID(id) || kind == "" || seen[id] {
			return History{}, ErrHistory
		}
		seen[id] = true
		if field(event, "room_id") != room {
			return History{}, ErrHistory
		}
		if kind == "m.room.message" {
			page.Events = append(page.Events, EventReference{Room: room, ID: id})
		}
	}
	return page, nil
}
