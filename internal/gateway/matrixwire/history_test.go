package matrixwire

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestHistoryOrderAndExplicitExhaustion(t *testing.T) {
	page, err := ParseHistory([]byte(`{"start":"Opaque/+世界","end":"next","chunk":[{"event_id":"$b","type":"m.room.message","room_id":"!room"},{"event_id":"$state","type":"m.room.name","room_id":"!room"},{"event_id":"$a","type":"m.room.message","room_id":"!room"}],"state":[]}`), "!room")
	if err != nil || page.Exhausted || page.Start != "Opaque/+世界" || page.End != "next" || len(page.Events) != 2 || page.Events[0].ID != "$b" || page.Events[1].ID != "$a" || page.Events[1].Room != "!room" {
		t.Fatal(page, err)
	}
	for _, end := range []string{"", `,"end":"next"`} {
		page, err = ParseHistory([]byte(`{"start":"s","chunk":[]`+end+`}`), "!room")
		if err != nil || page.Exhausted != (end == "") || len(page.Events) != 0 {
			t.Fatal("empty chunk guessed exhaustion", page, err)
		}
	}
}

func TestHistoryRefusesInvalidAndOversizedPages(t *testing.T) {
	for _, input := range []string{
		`null`, `{}`, `{"start":"s","chunk":[{"event_id":"$a","type":"m.room.message"}]}`, `{"start":null,"chunk":[]}`, `{"start":"s","chunk":null}`,
		`{"start":"s","chunk":[] ,"end":null}`, `{"start":"s","chunk":[],"end":"s"}`,
		`{"start":"s","chunk":[],"end":" "}`, `{"start":"s","chunk":[],"start":"other"}`,
		`{"start":"s","chunk":[null]}`, `{"start":"s","chunk":[{"event_id":"$a","type":"m.room.message","room_id":"!other"}]}`,
		`{"start":"s","chunk":[{"event_id":"$a","type":"m.room.message","room_id":"!room"},{"event_id":"$a","type":"m.room.name","room_id":"!room"}]}`,
		`{"start":"s","chunk":[{"event_id":"bad","type":"m.room.message","room_id":"!room"}]}`,
		`{"start":"s","chunk":[{"event_id":"$a","type":null}]}`,
		`{"start":"s","chunk":[],"state":"` + strings.Repeat("x", 65536) + `"}`,
		`{"start":"s","chunk":[]} {}`,
	} {
		if page, err := ParseHistory([]byte(input), "!room"); err == nil || page.Start != "" {
			t.Fatal("invalid page accepted", input[:min(len(input), 100)], page)
		}
	}
	if _, err := ParseHistory([]byte(`{"start":"s","chunk":[]}`), "#alias"); err == nil {
		t.Fatal("room alias accepted")
	}
	for _, n := range []int{256, 257} {
		chunk := make([]map[string]string, n)
		for i := range chunk {
			chunk[i] = map[string]string{"event_id": fmt.Sprintf("$%d", i), "type": "m.room.message", "room_id": "!room"}
		}
		data, _ := json.Marshal(map[string]any{"start": "s", "chunk": chunk})
		page, err := ParseHistory(data, "!room")
		if (err == nil) != (n == 256) || err == nil && len(page.Events) != n {
			t.Fatal(n, page, err)
		}
	}
}
