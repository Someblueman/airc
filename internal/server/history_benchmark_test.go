package server

import (
	"fmt"
	"testing"
)

func BenchmarkHistory(b *testing.B) {
	ring := newHistory(10000)
	for i := 0; i < 10000; i++ {
		target, body := "#other", "Progress: calibrated negatives, continuing useful work"
		if i%8 == 0 {
			target, body = "#research", "@worker assignment ready; report when complete"
		}
		ring.add(Message{ID: fmt.Sprintf("m%d", i), Seq: uint64(i + 1), From: "planner", Target: target, Body: body})
	}
	for _, query := range []struct{ name, target, after string }{
		{"RecentChannel", "#research", ""},
		{"MentionInbox", "@worker", ""},
		{"EmptyInbox", "@absent", ""},
		{"OldCursor", "#research", "m0"},
	} {
		b.Run(query.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				ring.since(query.target, query.after, 20)
			}
		})
	}
}
