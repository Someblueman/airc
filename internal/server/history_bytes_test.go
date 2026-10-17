package server

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// fillWindow adds n messages shaped like agent traffic: rooms, direct
// messages, mentions, replies and request IDs, with bodyLen-byte bodies.
func fillWindow(h *historyRing, n, bodyLen int) {
	body := strings.Repeat("status: tests pass, continuing with the next task ", bodyLen/50+1)[:bodyLen]
	for i := range n {
		// Each body is its own allocation, as a parsed message's is.
		m := Message{ID: fmt.Sprintf("%032x", i+1), Seq: uint64(i + 1), From: fmt.Sprintf("agent-%d", i%500), Target: fmt.Sprintf("#room-%d", i%20), Body: strings.Clone(body)}
		switch i % 10 {
		case 0:
			m.Target = fmt.Sprintf("agent-%d", (i+7)%500)
		case 1:
			m.Body = fmt.Sprintf("@agent-%d %s", (i+3)%500, body)
		case 2:
			m.ReplyTo = fmt.Sprintf("%032x", i)
			m.ThreadID = m.ReplyTo
		}
		if i%3 == 0 {
			m.RequestID = fmt.Sprintf("req-%d", i)
		}
		h.add(m)
	}
}

func heapInUse() uint64 {
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

// The byte bound is what an operator sizes memory by, so the estimate must
// stay close to what the window really holds on the heap.
func TestHistoryBytesTrackHeap(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates a large window")
	}
	for _, bodyLen := range []int{40, 300, 2000} {
		before := heapInUse()
		h := newHistoryWindow(maxHistoryMessages, 0)
		fillWindow(&h, 100_000, bodyLen)
		used := heapInUse() - before
		ratio := float64(used) / float64(h.bytes)
		t.Logf("body %d: estimate %d MiB, heap %d MiB, ratio %.2f", bodyLen, h.bytes>>20, used>>20, ratio)
		if ratio < 0.8 || ratio > 1.15 {
			t.Errorf("body %d: heap %d is %.2fx the estimate %d", bodyLen, used, ratio, h.bytes)
		}
		runtime.KeepAlive(&h)
	}
}

func TestHistoryByteBoundEvictsOldest(t *testing.T) {
	h := newHistoryWindow(maxHistoryMessages, 1<<20)
	fillWindow(&h, 20_000, 300)
	if h.bytes > 1<<20 || h.size == 0 || h.size >= 20_000 {
		t.Fatalf("window holds %d messages in %d bytes", h.size, h.bytes)
	}
	newest, _ := h.last()
	oldest := h.at(0)
	if newest.Seq != 20_000 || oldest.Seq != uint64(20_000-h.size+1) {
		t.Fatalf("window spans %d..%d with %d messages", oldest.Seq, newest.Seq, h.size)
	}
	if len(h.positions) != h.size {
		t.Fatalf("%d cursors for %d messages", len(h.positions), h.size)
	}
	for key, l := range h.index {
		if h.get(l.at(0)) == nil {
			t.Fatalf("index %s starts at a removed message", key)
		}
	}
}
