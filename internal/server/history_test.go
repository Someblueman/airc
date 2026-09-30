package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fillRing(limit int, targets ...string) (historyRing, []Message) {
	ring := newHistory(limit)
	var all []Message
	for i, target := range targets {
		message := Message{ID: fmt.Sprintf("m%d", i), Seq: uint64(i + 1), Target: target, Body: fmt.Sprint(i), Timestamp: time.Unix(int64(i), 0)}
		ring.add(message)
		all = append(all, message)
	}
	return ring, all
}

func ids(messages []Message) string {
	out := make([]string, len(messages))
	for i, message := range messages {
		out[i] = message.ID
	}
	return strings.Join(out, ",")
}

func TestHistorySinceFiltersTargetsAndPagesForward(t *testing.T) {
	ring, _ := fillRing(16, "#a", "#b", "#a", "bob", "#a", "#a", "Bob")

	got, status := ring.since("#a", "", 2)
	if ids(got) != "m4,m5" || status != historyOK {
		t.Fatalf("latest without cursor = %s %s", ids(got), status)
	}
	got, status = ring.since("#a", "m0", 2)
	if ids(got) != "m2,m4" || status != historyMore {
		t.Fatalf("first page = %s %s, want the oldest two after the cursor", ids(got), status)
	}
	got, status = ring.since("#a", "m4", 2)
	if ids(got) != "m5" || status != historyOK {
		t.Fatalf("last page = %s %s", ids(got), status)
	}
	got, status = ring.since("#a", "m5", 2)
	if len(got) != 0 || status != historyOK {
		t.Fatalf("caught-up cursor = %s %s", ids(got), status)
	}
	// Nicknames match case-insensitively; channels do not.
	if got, _ = ring.since("BOB", "", 10); ids(got) != "m3,m6" {
		t.Fatalf("direct messages = %s", ids(got))
	}
	if got, _ = ring.since("#A", "", 10); len(got) != 0 {
		t.Fatalf("channel names must be case-sensitive, got %s", ids(got))
	}
}

func TestHistorySinceReportsExpiredCursor(t *testing.T) {
	ring, _ := fillRing(3, "#a", "#a", "#a", "#a", "#a")
	got, status := ring.since("#a", "m0", 10)
	if status != historyExpired || ids(got) != "m2,m3,m4" {
		t.Fatalf("expired cursor = %s %s, want the latest messages flagged expired", ids(got), status)
	}
	if _, status = ring.since("#a", "never-existed", 10); status != historyExpired {
		t.Fatalf("unknown cursor status = %s", status)
	}
}

func TestRestoreHistoryCompactsAndContinuesSequence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	var lines []string
	for i := 1; i <= 20; i++ {
		lines = append(lines, fmt.Sprintf(`{"id":"id%d","seq":%d,"from":"a","target":"#c","message":"m%d","timestamp":"2026-01-01T00:00:00Z"}`, i, i, i))
		if i == 7 {
			lines = append(lines, "not json", `{"id":"","target":""}`)
		}
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := New(Config{HistoryLimit: 5})
	if err := srv.RestoreHistory(path); err != nil {
		t.Fatal(err)
	}
	defer srv.Shutdown(context.Background())
	got, _ := srv.history.since("#c", "", 100)
	if ids(got) != "id16,id17,id18,id19,id20" {
		t.Fatalf("restored %s", ids(got))
	}
	srv.mu.Lock()
	next := srv.newMessage("a", "#c", "after restart")
	srv.recordLocked(next)
	srv.mu.Unlock()
	if next.Seq != 21 {
		t.Fatalf("sequence after restore = %d, want 21", next.Seq)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(data), "\n"); count != 6 {
		t.Fatalf("history file has %d lines, want 5 compacted + 1 appended:\n%s", count, data)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("history file mode = %04o", info.Mode().Perm())
	}
}

func TestRestoreHistoryRequiresRetention(t *testing.T) {
	if err := New(Config{}).RestoreHistory(filepath.Join(t.TempDir(), "h")); err == nil {
		t.Fatal("history file without --history should be rejected")
	}
}
