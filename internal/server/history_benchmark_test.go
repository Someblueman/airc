package server

import (
	"fmt"
	"runtime"
	"testing"
)

func BenchmarkHistory(b *testing.B) {
	ring := newHistory(10000)
	for i := range 10000 {
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

// BenchmarkHistoryFullWindow reads a 1 GiB window (about 1.2 million
// messages from 500 agents in 20 rooms), the default bound, and shows that
// reads cost the page they return rather than the window.
func BenchmarkHistoryFullWindow(b *testing.B) {
	h := newHistoryWindow(maxHistoryMessages, defaultHistoryBytes)
	fillWindow(&h, 1_250_000, 300)
	newest, _ := h.last()
	middle := h.at(h.size / 2).ID
	b.Logf("window: %d messages, %d MiB", h.size, h.bytes>>20)
	runtime.GC() // measure steady state, not the collection after the fill
	for _, query := range []struct{ name, target, after string }{
		{"RecentRoom", "#room-3", ""},
		{"RecentInbox", "@agent-42", ""},
		{"EmptyInbox", "@absent", ""},
		{"CaughtUpRoom", "#room-3", newest.ID},
		{"RoomFromMiddle", "#room-3", middle},
		{"InboxFromMiddle", "@agent-42", middle},
	} {
		b.Run(query.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				h.since(query.target, query.after, 50)
			}
		})
	}
	b.Run("SearchWholeWindow", func(b *testing.B) {
		s := New(Config{HistoryLimit: 1})
		s.history = h
		matcher := newSearchText("no such text")
		s.mu.Lock()
		for b.Loop() {
			s.readAfterLocked("*", "*", 50, func(e *entry) bool { return matcher.contains(e.Body) })
		}
		s.mu.Unlock()
		h = s.history
	})
	b.Run("Add", func(b *testing.B) {
		b.ReportAllocs()
		i := 2_000_000
		for b.Loop() {
			h.add(Message{ID: fmt.Sprintf("%032x", i), Seq: uint64(i), From: "agent-1", Target: "#room-1", Body: "@agent-2 status: tests pass"})
			i++
		}
	})
}
