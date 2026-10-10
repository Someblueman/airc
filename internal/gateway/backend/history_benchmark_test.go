package backend

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func BenchmarkValidHistoryID(b *testing.B) {
	for _, test := range []struct{ name, id string }{
		{"lowercase", "0123456789abcdef0123456789abcdef"},
		{"uppercase", "0123456789ABCDEF0123456789ABCDEF"},
		{"invalid-first", "z" + strings.Repeat("a", 31)},
		{"invalid-last", strings.Repeat("a", 31) + "z"},
	} {
		b.Run(test.name, func(b *testing.B) {
			if len(test.id) != 32 {
				b.Fatalf("ID has %d bytes", len(test.id))
			}
			b.ReportAllocs()
			for b.Loop() {
				validHistoryID(test.id)
			}
		})
	}
}

func BenchmarkHistoryPage(b *testing.B) {
	for _, count := range []int{1, 50} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			r := core.Request{Target: &core.Target{Kind: "room", Name: "#room"}, Limit: count}
			entries := make([]irc.CheckEntry, 0, count+1)
			for i := range count {
				message := &irc.MessageMetadata{
					ID: fmt.Sprintf("%032x", i), Target: "#room", From: "author",
					Message: strings.Repeat("x", 256), Timestamp: time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC),
				}
				entries = append(entries, irc.CheckEntry{Kind: "message", Target: "#room", Targets: []int{0}, Message: message})
			}
			entries = append(entries, irc.CheckEntry{Kind: "page", Target: "#room", Status: "ok", Cursor: entries[count-1].Message.ID})
			b.ReportAllocs()
			for b.Loop() {
				if _, err := historyPage(entries, r); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
