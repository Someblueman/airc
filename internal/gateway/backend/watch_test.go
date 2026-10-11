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

func TestWatchLimitsReserveSendCapacityAndRelease(t *testing.T) {
	b := &Backend{globalLimit: 3, authorLimit: 3, watchLimit: 2, authorWatchLimit: 1, active: map[string]int{}, lookupActive: map[string]int{}, watchActive: map[string]int{}}
	if !b.acquireWatch("one") || b.acquireWatch("one") || !b.acquireWatch("two") || b.acquireWatch("three") {
		t.Fatal("watch limits bypassed")
	}
	if !b.acquire("one", store.SendAttempt) {
		t.Fatal("watches consumed send reserve")
	}
	b.release("one", store.SendAttempt)
	b.releaseWatch("one")
	if !b.acquireWatch("one") {
		t.Fatal("watch slot leaked")
	}
	b.releaseWatch("one")
	b.releaseWatch("two")
	if b.total != 0 || b.lookupTotal != 0 || b.watchTotal != 0 {
		t.Fatal("session counters leaked", b)
	}
}

func TestWatchSnapshotOverlapAndOverflow(t *testing.T) {
	r := core.Request{Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#room"}, Limit: 2}
	one := &irc.MessageMetadata{ID: strings.Repeat("a", 32), From: "author", Target: "#room", Message: "original", Timestamp: time.Now()}
	one.Seq, one.Receipt = 7, &irc.ReceiptInfo{Accepted: true, Persisted: true}
	liveOne := *one
	liveOne.Seq, liveOne.Receipt = 0, nil
	two := *one
	two.ID, two.Message = strings.Repeat("b", 32), "live"
	page := HistoryPage{Messages: []*irc.MessageMetadata{one}, Cursor: one.ID, Status: "ok"}
	got, err := mergeWatch(page, []*irc.MessageMetadata{&liveOne, &two}, r)
	if err != nil || len(got.Messages) != 2 || got.Cursor != two.ID || got.Messages[0].Seq != 7 || got.Messages[0].Receipt != one.Receipt {
		t.Fatal("overlap lost or duplicated", got, err)
	}
	r.Limit = 1
	if got, err := mergeWatch(page, []*irc.MessageMetadata{&two}, r); !errors.Is(err, store.ErrCapacity) || len(got.Messages) != 0 {
		t.Fatal("partial overflow", got, err)
	}
	r.Limit = 2
	two.Target = "#foreign"
	if got, err := mergeWatch(page, []*irc.MessageMetadata{&two}, r); err == nil || len(got.Messages) != 0 {
		t.Fatal("foreign live record leaked", got, err)
	}
	two = *one
	two.Message = "changed under same ID"
	if got, err := mergeWatch(page, []*irc.MessageMetadata{&two}, r); !errors.Is(err, ErrReadUnavailable) || len(got.Messages) != 0 {
		t.Fatal("duplicate hid changed immutable message", got, err)
	}
	for _, prefix := range []HistoryPage{
		{Messages: []*irc.MessageMetadata{one}, Cursor: one.ID, Status: "more"},
		{Messages: []*irc.MessageMetadata{one}, Cursor: one.ID, Status: "ok", Gap: true},
	} {
		if got, err := mergeWatch(prefix, []*irc.MessageMetadata{&two}, r); !errors.Is(err, ErrReadUnavailable) || len(got.Messages) != 0 {
			t.Fatal("prefix/gap skipped immutable overlap correlation", got, err)
		}
		newLive := liveOne
		newLive.ID = strings.Repeat("c", 32)
		got, err := mergeWatch(prefix, []*irc.MessageMetadata{&newLive}, r)
		if err != nil || len(got.Messages) != 1 || got.Cursor != one.ID || got.Status != prefix.Status || got.Gap != prefix.Gap {
			t.Fatal("deferred live event advanced unread prefix", got, err)
		}
	}
}
