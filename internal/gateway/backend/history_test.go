package backend

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestHistoryPageRejectsForeignMalformedAndOversizedSnapshots(t *testing.T) {
	r := core.Request{Target: &core.Target{Kind: "room", Name: "#room"}, Limit: 50}
	m := irc.MessageMetadata{ID: strings.Repeat("a", 32), Target: "#room", From: "author", Message: "body", Timestamp: time.Now()}
	message := irc.CheckEntry{Kind: "message", Target: "#room", Targets: []int{0}, Message: &m}
	end := irc.CheckEntry{Kind: "page", Target: "#room", Status: "ok", Cursor: m.ID}
	for _, test := range []struct {
		name    string
		change  func(*irc.CheckEntry, *irc.CheckEntry)
		missing bool
	}{
		{"foreign marker", func(m, e *irc.CheckEntry) { e.Target = "#other" }, false},
		{"foreign body", func(m, e *irc.CheckEntry) { m.Message.Target = "#other" }, false},
		{"invalid id", func(m, e *irc.CheckEntry) { m.Message.ID = "bad" }, false},
		{"bad association", func(m, e *irc.CheckEntry) { m.Targets = []int{1} }, false},
		{"bad status", func(m, e *irc.CheckEntry) { e.Status = "unknown" }, false},
		{"skipped cursor", func(m, e *irc.CheckEntry) { e.Status = "more"; e.Cursor = strings.Repeat("b", 32) }, false},
		{"missing cursor", func(m, e *irc.CheckEntry) { e.Cursor = "" }, false},
		{"missing marker", func(m, e *irc.CheckEntry) {}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			copy := m
			msg, marker := message, end
			msg.Message = &copy
			test.change(&msg, &marker)
			entries := []irc.CheckEntry{msg}
			if !test.missing {
				entries = append(entries, marker)
			}
			if page, err := historyPage(entries, r); err == nil || len(page.Messages) != 0 {
				t.Fatal("unsafe snapshot returned", page, err)
			}
		})
	}
	var entries []irc.CheckEntry
	for i := range 20 {
		copy := m
		copy.ID = strings.Repeat("a", 30) + string("0123456789abcdef"[i/16]) + string("0123456789abcdef"[i%16])
		copy.Message = strings.Repeat("x", 4096)
		msg := message
		msg.Message = &copy
		entries = append(entries, msg)
	}
	entries = append(entries, end)
	if page, err := historyPage(entries, r); !errors.Is(err, store.ErrCapacity) || len(page.Messages) != 0 {
		t.Fatal("oversized snapshot returned", page, err)
	}
}
