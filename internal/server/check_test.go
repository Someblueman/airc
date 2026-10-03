package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/protocol"
)

func TestCombinedCheckBoundsOverlapsAndWireSize(t *testing.T) {
	s := New(Config{HistoryLimit: 100})
	root := s.newMessage("writer", "#room", strings.Repeat("x", 4096), nil)
	s.history.add(root)
	r := protocol.CheckRequest{MaxMessages: 1, IncludeOwn: true, Headers: true}
	r.Targets = append(r.Targets, protocol.CheckTarget{Target: "#room", After: "*", Limit: 1000})
	for i := 0; i < 63; i++ {
		m := s.newMessage("writer", "#room", fmt.Sprint(i), &root)
		s.history.add(m)
		r.Targets = append(r.Targets, protocol.CheckTarget{Target: "thread:" + m.ID, After: "*", Limit: 1000})
	}
	actor := &session{client: Client{Nick: "reader"}, out: make(chan string, 2048), done: make(chan struct{})}
	data, _ := json.Marshal(r)
	if len(data)+7 > protocol.MaxLineLength {
		t.Fatal("maximum request exceeds wire limit")
	}
	s.checkLocked(actor, protocol.Command{Trailing: string(data)})
	messages, pages := 0, 0
	for len(actor.out) > 0 {
		line := <-actor.out
		if len(line) > protocol.MaxLineLength+2 {
			t.Fatal("response exceeds wire limit")
		}
		c, err := protocol.Parse(line)
		if err != nil {
			t.Fatal(err)
		}
		if c.Name == "784" {
			var e protocol.CheckEntry
			if err := json.Unmarshal([]byte(c.Trailing), &e); err != nil {
				t.Fatal(err)
			}
			if e.Kind == "message" {
				messages++
				if e.Message.ID != root.ID || len(e.Targets) != 64 {
					t.Fatal("overlapping snapshot changed", e)
				}
			}
			if e.Kind == "page" {
				pages++
				if e.Status != "more" || e.Cursor != root.ID {
					t.Fatal("cursor skipped an omitted message", e)
				}
			}
		}
	}
	if messages != 1 || pages != 64 {
		t.Fatalf("messages=%d pages=%d", messages, pages)
	}
}

func TestCombinedCheckRejectsDuplicateTargetsBeforeOutput(t *testing.T) {
	s := New(Config{HistoryLimit: 10})
	actor := &session{client: Client{Nick: "reader"}, out: make(chan string, 8), done: make(chan struct{})}
	r := protocol.CheckRequest{MaxMessages: 10, Headers: true, Targets: []protocol.CheckTarget{{Target: "#room", Limit: 10}, {Target: "#room", Limit: 10}}}
	data, _ := json.Marshal(r)
	s.checkLocked(actor, protocol.Command{Trailing: string(data)})
	if len(actor.out) != 1 || !containsNumeric(<-actor.out, "461") {
		t.Fatal("partial snapshot before rejection")
	}
}
