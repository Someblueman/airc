package matrixwire

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func TestMatrixTimelineReferencesGapsAndDepartures(t *testing.T) {
	raw := `{"next_batch":"Opaque/+世界","rooms":{"join":{"!room":{"timeline":{"limited":true,"prev_batch":"Before/+","events":[{"type":"m.room.message","event_id":"$First"},{"type":"m.room.member","event_id":"$State"},{"type":"m.room.message","event_id":"$Last"}]}}},"leave":{"!departed":{}}}}`
	p, err := ParseTimeline([]byte(raw))
	if err != nil || p.Next != "Opaque/+世界" || len(p.Events) != 2 || p.Events[0].ID != "$First" || p.Events[1].ID != "$Last" || len(p.Gaps) != 1 || p.Gaps[0].Before != "Before/+" || len(p.Departed) != 1 || p.Departed[0] != "!departed" {
		t.Fatal(p, err)
	}
	if p, err = ParseTimeline([]byte(`{"next_batch":"empty"}`)); err != nil || len(p.Events) != 0 {
		t.Fatal(p, err)
	}
}

func TestMatrixTimelineRefusalsAndBounds(t *testing.T) {
	for _, raw := range []string{
		`null`, `{"next_batch":""}`, `{"next_batch":"a b"}`, `{"next_batch":"a","rooms":null}`,
		`{"next_batch":"a","rooms":{"join":{"#alias":{}}}}`,
		`{"next_batch":"a","rooms":{"join":{"!r":{}},"leave":{"!r":{}}}}`,
		`{"next_batch":"a","rooms":{"join":{"!r":{"timeline":{"events":null}}}}}`,
		`{"next_batch":"a","rooms":{"join":{"!r":{"timeline":{"limited":null,"events":[]}}}}}`,
		`{"next_batch":"a","rooms":{"join":{"!r":{"timeline":{"limited":true,"events":[]}}}}}`,
		`{"next_batch":"a","rooms":{"join":{"!r":{"timeline":{"events":[{"event_id":"$e","type":"m.room.message","room_id":"!other"}]}}}}}`,
		`{"next_batch":"a","rooms":{"join":{"!r":{"timeline":{"events":[{"event_id":"$e","type":"m.room.message"},{"event_id":"$e","type":"m.room.message"}]}}}}}`,
		`{"next_batch":"a","next_batch":"b"}`, strings.Repeat(" ", 65536) + `{"next_batch":"a"}`,
	} {
		if _, err := ParseTimeline([]byte(raw)); err == nil {
			t.Fatal("invalid page accepted", raw)
		}
	}
	for _, n := range []int{256, 257} {
		events := make([]any, 0, n)
		for i := range n {
			events = append(events, map[string]any{"type": "m.room.message", "event_id": "$" + strconv.Itoa(i)})
		}
		raw, _ := json.Marshal(map[string]any{"next_batch": "a", "rooms": map[string]any{"join": map[string]any{"!r": map[string]any{"timeline": map[string]any{"events": events}}}}})
		if _, err := ParseTimeline(raw); (err == nil) != (n == 256) {
			t.Fatal(n, err)
		}
	}
	for _, s := range []string{"", "a\n", strings.Repeat("x", 1025)} {
		if Cursor(s) {
			t.Fatal("bad cursor accepted")
		}
	}
}

func TestMatrixSyncRoomAndNestedMetadataBounds(t *testing.T) {
	for _, n := range []int{256, 257} {
		rooms := map[string]any{}
		for i := range n {
			rooms["!room"+strconv.Itoa(i)] = map[string]any{}
		}
		raw, _ := json.Marshal(map[string]any{"next_batch": "a", "rooms": map[string]any{"join": rooms}})
		if _, err := ParseTimeline(raw); (err == nil) != (n == 256) {
			t.Fatal("room bound", n, err)
		}
	}
	raw := []byte(`{"next_batch":"a","rooms":{"join":{"!r":{"timeline":{"events":[{"type":"m.room.message","event_id":"$e","content":{"extension":{"nested":{"more":["metadata"]}}}}]}}}}}`)
	if _, err := ParseTimeline(raw); err != nil {
		t.Fatal("bounded nested sync metadata refused", err)
	}
}
