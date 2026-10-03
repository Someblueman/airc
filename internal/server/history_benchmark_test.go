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

// Insertion into a full ring with one room quota set, the case that used to
// rescan and shift the whole ring on every post.
func BenchmarkHistoryAddWithQuota(b *testing.B) {
	ring := newHistory(10000)
	ring.quotas = map[string]roomSettings{"#quiet": {HistoryLimit: 100}}
	add := func(i int) {
		target := "#busy"
		if i%10 == 0 {
			target = "#quiet"
		}
		ring.add(Message{ID: fmt.Sprintf("m%d", i), Seq: uint64(i + 1), From: "planner", Target: target, Body: "status"})
	}
	for i := range 10000 {
		add(i)
	}
	next := 10000
	b.ReportAllocs()
	for b.Loop() {
		add(next)
		next++
	}
}
