package matrixwire

import (
	"encoding/json"
	"errors"
	"slices"
	"unicode"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/gateway/jsoninput"
)

var ErrTimeline = errors.New("invalid Matrix timeline page")

type EventReference struct{ Room, ID string }
type TimelineGap struct{ Room, Before string }

// Timeline is parsed data only. Next is not an acknowledgement or permission
// to advance intake. Gaps and departures require explicit owner handling.
type Timeline struct {
	Next     string
	Events   []EventReference
	Gaps     []TimelineGap
	Departed []string
}

func Cursor(s string) bool {
	if len(s) < 1 || len(s) > 1024 || !utf8.ValidString(s) {
		return false
	}
	for _, c := range s {
		if unicode.IsControl(c) || unicode.IsSpace(c) {
			return false
		}
	}
	return true
}

// ParseTimeline bounds the entire sync page to 64 KiB, nesting to 16, rooms to
// 256 and timeline events to 256. It preserves each room's event order; there
// is no inferred cross-room chronology. Ancillary state is not event authority.
func ParseTimeline(data []byte) (Timeline, error) {
	if jsoninput.ValidateDepth(data, 65536, 16) != nil {
		return Timeline{}, ErrTimeline
	}
	root, ok := object(data)
	if !ok || !Cursor(field(root, "next_batch")) {
		return Timeline{}, ErrTimeline
	}
	page := Timeline{Next: field(root, "next_batch")}
	raw, exists := root["rooms"]
	if !exists {
		return page, nil
	}
	rooms, ok := object(raw)
	if !ok {
		return Timeline{}, ErrTimeline
	}
	seenRooms := map[string]bool{}
	seenEvents := map[string]bool{}
	count := 0
	for _, category := range []string{"join", "leave"} {
		raw, exists := rooms[category]
		if !exists {
			continue
		}
		entries, ok := object(raw)
		if !ok {
			return Timeline{}, ErrTimeline
		}
		ordered := make([]string, 0, len(entries))
		for room := range entries {
			ordered = append(ordered, room)
		}
		slices.Sort(ordered)
		for _, room := range ordered {
			if !RoomID(room) || seenRooms[room] || len(seenRooms) >= 256 {
				return Timeline{}, ErrTimeline
			}
			seenRooms[room] = true
			entry, ok := object(entries[room])
			if !ok {
				return Timeline{}, ErrTimeline
			}
			if category == "leave" {
				page.Departed = append(page.Departed, room)
				continue
			}
			raw, exists := entry["timeline"]
			if !exists {
				continue
			}
			timeline, ok := object(raw)
			if !ok {
				return Timeline{}, ErrTimeline
			}
			limited := false
			if raw, exists := timeline["limited"]; exists {
				if string(raw) != "true" && string(raw) != "false" {
					return Timeline{}, ErrTimeline
				}
				limited = string(raw) == "true"
			}
			before := ""
			if _, exists := timeline["prev_batch"]; exists {
				before = field(timeline, "prev_batch")
				if !Cursor(before) {
					return Timeline{}, ErrTimeline
				}
			}
			if limited {
				if before == "" {
					return Timeline{}, ErrTimeline
				}
				page.Gaps = append(page.Gaps, TimelineGap{Room: room, Before: before})
			}
			var events []json.RawMessage
			if json.Unmarshal(timeline["events"], &events) != nil || events == nil {
				return Timeline{}, ErrTimeline
			}
			count += len(events)
			if count > 256 {
				return Timeline{}, ErrTimeline
			}
			for _, raw := range events {
				event, ok := object(raw)
				if !ok {
					return Timeline{}, ErrTimeline
				}
				id, kind := field(event, "event_id"), field(event, "type")
				if !EventID(id) || kind == "" || seenEvents[id] {
					return Timeline{}, ErrTimeline
				}
				seenEvents[id] = true
				if _, exists := event["room_id"]; exists && field(event, "room_id") != room {
					return Timeline{}, ErrTimeline
				}
				if kind == "m.room.message" {
					page.Events = append(page.Events, EventReference{Room: room, ID: id})
				}
			}
		}
	}
	return page, nil
}
