package backend

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
)

// fitCheck cuts the newest messages until a result fits, keeping each page
// a prefix: a page cut short says "more" from its last kept message, or
// from where it started when it keeps none.
func TestFitCheckKeepsPrefixes(t *testing.T) {
	id := func(i int) string { return fmt.Sprintf("%032x", i+1) }
	start := fmt.Sprintf("%032x", 999)
	pages := []core.CheckPage{{Room: "#a", Messages: []*irc.MessageMetadata{}, Status: "ok"}, {Room: "#b", Messages: []*irc.MessageMetadata{}, Status: "ok"}}
	var order []*irc.MessageMetadata
	routed := make([][]string, 2)
	for i := range 40 {
		room := i % 2
		if i >= 30 {
			room = 1 // #b alone holds the newest ten
		}
		m := &irc.MessageMetadata{ID: id(i), From: "poster", Target: pages[room].Room, Message: strings.Repeat("x", 3000), Timestamp: time.Unix(int64(i), 0)}
		pages[room].Messages = append(pages[room].Messages, m)
		order = append(order, m)
		routed[room] = append(routed[room], m.ID)
	}
	size := func() int {
		data, _ := json.Marshal(core.CheckResult{Rooms: pages})
		return len(data)
	}
	if size() <= core.MaxCheckBytes {
		t.Fatal("the fixture fits already")
	}
	if !fitCheck(pages, order, routed, []string{start, start}, size()) {
		t.Fatal("did not fit")
	}
	if size() > core.MaxCheckBytes {
		t.Fatalf("still %d bytes", size())
	}
	kept := map[string]bool{}
	for _, page := range pages {
		for _, m := range page.Messages {
			kept[m.ID] = true
		}
	}
	for i, m := range order {
		if kept[m.ID] != (i < len(kept)) {
			t.Fatalf("message %d kept %v: the kept messages are not the oldest", i, kept[m.ID])
		}
	}
	for _, page := range pages {
		if page.Status != "more" || page.Cursor != page.Messages[len(page.Messages)-1].ID {
			t.Fatalf("%s: %s at %s", page.Room, page.Status, page.Cursor)
		}
	}

	// #a holds the oldest 25, which alone fill the answer: #b keeps nothing
	// and starts again where it was.
	pages = []core.CheckPage{{Room: "#a", Status: "ok"}, {Room: "#b", Status: "ok"}}
	routed = make([][]string, 2)
	for i, m := range order {
		room := 0
		if i >= 25 {
			room = 1
		}
		m.Target = pages[room].Room
		pages[room].Messages = append(pages[room].Messages, m)
		routed[room] = append(routed[room], m.ID)
	}
	if !fitCheck(pages, order, routed, []string{start, start}, size()) || size() > core.MaxCheckBytes {
		t.Fatal("did not fit")
	}
	if pages[0].Status != "more" || len(pages[1].Messages) != 0 || pages[1].Status != "more" || pages[1].Cursor != start {
		t.Fatalf("#a %s at %s; #b %d messages, %s at %s", pages[0].Status, pages[0].Cursor, len(pages[1].Messages), pages[1].Status, pages[1].Cursor)
	}
}
