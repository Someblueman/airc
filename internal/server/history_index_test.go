package server

import (
	"fmt"
	"testing"

	"github.com/Someblueman/airc/internal/protocol"
)

func TestHistoryIndexesAndMentionCacheFollowRingEviction(t *testing.T) {
	ring := newHistory(3)
	for _, message := range []Message{
		{ID: "a", From: "planner", Target: "#room", Body: "@worker assignment"},
		{ID: "b", From: "worker", Target: "#room", Body: "@worker own post"},
		{ID: "c", From: "planner", Target: "WORKER", Body: "direct message"},
		{ID: "d", From: "planner", Target: "#other", Body: "@other unrelated"},
	} {
		ring.add(message)
	}
	if got := ring.recent("@WORKER", 20); ids(got) != "c" {
		t.Fatalf("cache retained an evicted tag or own mention: %s", ids(got))
	}
	if got, status := ring.since("@worker", "a", 20); status != historyExpired || ids(got) != "c" {
		t.Fatalf("evicted cursor = %s %s", ids(got), status)
	}
	if got, status := ring.since("@worker", "b", 20); status != historyOK || ids(got) != "c" {
		t.Fatalf("wrapped cursor = %s %s", ids(got), status)
	}
	ring.add(Message{ID: "e", From: "planner", Target: "#room", Body: "Worker: next assignment"})
	if got := ring.recent("@worker", 1); ids(got) != "e" {
		t.Fatalf("newest bounded inbox = %s", ids(got))
	}
	if got, status := ring.since("@worker", "*", 1); status != historyMore || ids(got) != "c" {
		t.Fatalf("oldest bounded inbox = %s %s", ids(got), status)
	}
}

func TestDMAuditHistoryExcludesChannelsAndReportsEviction(t *testing.T) {
	ring, _ := fillRing(3, "#room", "muse", "planner", "#room", "muse")
	if got, status := ring.since(protocol.AllDirectMessages, "m0", 10); ids(got) != "m2,m4" || status != historyExpired {
		t.Fatalf("expired audit = %s %s", ids(got), status)
	}
	if got, status := ring.since(protocol.AllDirectMessages, "*", 1); ids(got) != "m2" || status != historyMore {
		t.Fatalf("oldest audit page = %s %s", ids(got), status)
	}
	if got, status := ring.since(protocol.AllDirectMessages, "m2", 1); ids(got) != "m4" || status != historyOK {
		t.Fatalf("next audit page = %s %s", ids(got), status)
	}
}

func TestHistoryCursorIndexRemainsBoundedAcrossManyWraps(t *testing.T) {
	ring := newHistory(7)
	for i := 0; i < 1000; i++ {
		ring.add(Message{ID: fmt.Sprint(i), Target: "#room"})
		if len(ring.positions) > 7 {
			t.Fatalf("cursor metadata grew beyond retention: %d", len(ring.positions))
		}
	}
	if got, status := ring.since("#room", "995", 2); status != historyMore || ids(got) != "996,997" {
		t.Fatalf("index after many wraps = %s %s", ids(got), status)
	}
	if _, status := ring.since("#room", "992", 2); status != historyExpired {
		t.Fatalf("old index survived eviction: %s", status)
	}
}
