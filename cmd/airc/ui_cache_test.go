package main

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

func uncachedLog(b *uiBuffer, width int) []string {
	r := newRenderer(true, width, b.kind != bufChannel)
	for _, event := range b.items {
		if message, ok := event.(*irc.MessageEvent); ok {
			r.reserve(message.From)
		}
	}
	var lines []string
	for _, event := range b.items {
		if text := strings.TrimRight(r.render(event), "\n"); text != "" {
			lines = append(lines, strings.Split(text, "\n")...)
		}
	}
	if len(lines) == 0 {
		lines = []string{sty("2", " Nothing here yet.")}
	}
	return lines
}

func TestUIRenderedRecordsMatchFreshRendering(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	m := newUIModel("human", []string{"#room"}, func() time.Time { return now })
	b := m.cur()
	check := func(width int) {
		t.Helper()
		got, want := m.logLines(b, width), uncachedLog(b, width)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("render differs at width %d", width)
		}
		if len(b.cacheEvents) > len(b.items) {
			t.Fatal("evicted records remain cached")
		}
	}
	check(110)
	for i := range bufferLimit {
		b.add(&irc.MessageEvent{ID: fmt.Sprint(i), From: "writer", Target: "#room", Message: "**hello**\n```go\nx := 1\n```", Timestamp: now.Add(time.Duration(i-500) * time.Hour)})
	}
	check(110)
	for i := range 6 {
		b.add(&irc.MessageEvent{ID: fmt.Sprint(500 + i), From: "longer-writer-name", Target: "#room", Message: "a newly appended message", Timestamp: now})
		check(110)
	}
	// Out-of-order history, corrections, retractions, notices and layout changes.
	b.add(&irc.MessageEvent{ID: "late", From: "writer", Target: "#room", Message: "older history", Timestamp: now.Add(-20 * time.Hour)})
	check(110)
	for _, kind := range []string{"correct", "retract"} {
		message := &irc.MessageEvent{ID: kind, From: "writer", Target: "#room", Message: "replacement", Timestamp: now}
		message.Kind, message.Supersedes = kind, "499"
		m.route(message, false)
		check(110)
	}
	b.add(&irc.JoinEvent{Agent: "guest", Channel: "#room", Timestamp: now})
	check(110)
	check(30)
	check(160)
	b.items, b.seen = nil, map[string]struct{}{}
	b.version++
	check(160)
}

func BenchmarkUIAppendRedraw(b *testing.B) {
	m := newUIModel("human", []string{"#room"}, time.Now)
	m.width, m.height = 160, 45
	for i := range bufferLimit {
		m.cur().add(&irc.MessageEvent{ID: fmt.Sprint(i), From: "writer", Target: "#room", Message: strings.Repeat("content ", 128), Timestamp: time.Now()})
	}
	m.view()
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		m.cur().add(&irc.MessageEvent{ID: fmt.Sprint(i + bufferLimit), From: "writer", Target: "#room", Message: strings.Repeat("content ", 128), Timestamp: time.Now()})
		m.view()
	}
}
