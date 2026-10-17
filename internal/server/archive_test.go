package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
)

// archiveServer opens a history file with a window of limit messages and
// segments of about segment bytes.
func archiveServer(t *testing.T, path string, limit int, segment int64, cfg Config) *Server {
	t.Helper()
	cfg.HistoryLimit = limit
	s := New(cfg)
	s.histSegment = segment
	if err := s.RestoreHistory(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	return s
}

func post(s *Server, from, target, body string) Message {
	s.messageMu.Lock()
	defer s.messageMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.newMessage(from, target, body, nil)
	s.recordLocked(&m)
	return m
}

// indexAll indexes every sealed segment now rather than waiting for the janitor.
func indexAll(t *testing.T, s *Server) {
	t.Helper()
	for _, seg := range s.archive.unindexed() {
		if err := s.archive.index(seg); err != nil {
			t.Fatal(err)
		}
	}
}

// readAll pages target from cursor to the end, as a client does.
func readAll(t *testing.T, s *Server, target, cursor string) []string {
	t.Helper()
	var got []string
	for range 10000 {
		s.mu.Lock()
		page, status, next, found := s.readAfterLocked(target, cursor, 7, nil)
		s.mu.Unlock()
		if !found {
			t.Fatalf("cursor %s not found", cursor)
		}
		for _, m := range page {
			got = append(got, m.Body)
		}
		if status != historyMore {
			return got
		}
		cursor = next
	}
	t.Fatal("paging did not finish")
	return nil
}

func TestArchiveResumesCursorsOlderThanTheWindow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	s := archiveServer(t, path, 5, 2048, Config{})
	var room []Message
	var inbox []string
	for i := range 300 {
		switch i % 3 {
		case 0:
			room = append(room, post(s, "alice", "#room", fmt.Sprint("room ", i)))
		case 1:
			post(s, "alice", "#other", fmt.Sprintf("@bob mention %d", i))
			inbox = append(inbox, fmt.Sprintf("@bob mention %d", i))
		case 2:
			post(s, "alice", "bob", fmt.Sprint("dm ", i))
			inbox = append(inbox, fmt.Sprint("dm ", i))
		}
	}
	want := func(from int) string {
		var out []string
		for _, m := range room[from+1:] {
			out = append(out, m.Body)
		}
		return strings.Join(out, ",")
	}
	cursor := room[3].ID // long gone from the five-message window
	check := func(stage string) {
		if got := strings.Join(readAll(t, s, "#room", cursor), ","); got != want(3) {
			t.Fatalf("%s: room from an archived cursor:\n%s\nwant\n%s", stage, got, want(3))
		}
		if got := strings.Join(readAll(t, s, "@bob", room[0].ID), ","); got != strings.Join(inbox, ",") {
			t.Fatalf("%s: inbox from an archived cursor:\n%s", stage, got)
		}
	}
	check("unindexed")
	indexAll(t, s)
	check("indexed")
	s.Shutdown(context.Background())
	s = archiveServer(t, path, 5, 2048, Config{})
	check("restarted")
	if s.seq != 300 {
		t.Fatalf("sequence after restart = %d", s.seq)
	}
}

func TestArchiveReadsAreBudgetedAndContinue(t *testing.T) {
	defer func(budget int64) { archiveBudget = budget }(archiveBudget)
	archiveBudget = 600 // a few messages per request
	s := archiveServer(t, filepath.Join(t.TempDir(), "history.jsonl"), 3, 4096, Config{})
	var first Message
	var want []string
	for i := range 100 {
		m := post(s, "alice", "#room", fmt.Sprint(i))
		if i == 0 {
			first = m
		} else {
			want = append(want, m.Body)
		}
		post(s, "alice", "#noise", "noise")
	}
	// Pages that run out of budget may hold nothing; paging still reaches
	// every message exactly once.
	if got := readAll(t, s, "#room", first.ID); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("budgeted paging:\n%s", strings.Join(got, ","))
	}
}

func TestArchiveSkipsSegmentsWithoutTheTarget(t *testing.T) {
	defer func(budget int64) { archiveBudget = budget }(archiveBudget)
	s := archiveServer(t, filepath.Join(t.TempDir(), "history.jsonl"), 3, 2048, Config{})
	first := post(s, "alice", "#busy", "start")
	for i := range 400 {
		post(s, "alice", "#busy", fmt.Sprint("busy ", i))
	}
	rare := post(s, "alice", "#rare", "the rare one")
	for range 10 {
		post(s, "alice", "#busy", "after")
	}
	indexAll(t, s)
	archiveBudget = 4096 // far less than the busy segments
	s.mu.Lock()
	page, status, _, found := s.readAfterLocked("#rare", first.ID, 10, nil)
	s.mu.Unlock()
	if !found || status != historyOK || len(page) != 1 || page[0].ID != rare.ID {
		t.Fatalf("rare room: %d messages, %s, found %v", len(page), status, found)
	}
}

func TestArchiveRetentionDeletesOldestSegments(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "history.jsonl")
	s := archiveServer(t, path, 3, 1024, Config{Now: func() time.Time { return now }, HistoryRetainBytes: 8 << 10, HistoryRetain: 24 * time.Hour})
	first := post(s, "alice", "#room", "first")
	for i := range 300 {
		post(s, "alice", "#room", fmt.Sprint("message ", i))
	}
	indexAll(t, s)
	s.expireHistory()
	segments, size := s.archive.stats()
	if size+s.histBytes > 8<<10 || segments == 0 {
		t.Fatalf("archive holds %d bytes in %d segments", size+s.histBytes, segments)
	}
	s.mu.Lock()
	_, _, _, found := s.readAfterLocked("#room", first.ID, 10, nil)
	s.mu.Unlock()
	if found {
		t.Fatal("a cursor in a deleted segment still resolved")
	}
	// Two days later everything sealed has expired, and so has the window.
	now = now.Add(48 * time.Hour)
	s.expireHistory()
	if segments, _ := s.archive.stats(); segments != 0 || s.history.size != 0 {
		t.Fatalf("after expiry: %d segments, %d in the window", segments, s.history.size)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("the active file was deleted")
	}
}

func TestArchiveRecoversFromAnInterruptedRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	s := archiveServer(t, path, 3, 1<<20, Config{})
	var last Message
	for i := range 10 {
		last = post(s, "alice", "#room", fmt.Sprint(i))
	}
	s.Shutdown(context.Background())
	// A crash after the sealed link was made but before the new file
	// replaced the active path leaves both names on one file.
	if err := os.Link(path, segmentPath(path, 1)); err != nil {
		t.Fatal(err)
	}
	s = archiveServer(t, path, 3, 1<<20, Config{})
	if segments, _ := s.archive.stats(); segments != 0 {
		t.Fatalf("%d sealed segments after recovery, want the duplicate removed", segments)
	}
	if _, err := os.Stat(segmentPath(path, 1)); !os.IsNotExist(err) {
		t.Fatal("duplicate link kept")
	}
	if got, ok := s.history.message(last.ID); !ok || !got.Persisted {
		t.Fatal("messages lost")
	}
}

func TestArchiveRebuildsADamagedIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	s := archiveServer(t, path, 3, 2048, Config{})
	first := post(s, "alice", "#room", "first")
	for i := range 100 {
		post(s, "alice", "#room", fmt.Sprint(i))
	}
	indexAll(t, s)
	s.Shutdown(context.Background())
	indexes, _ := filepath.Glob(path + ".*.idx")
	if len(indexes) == 0 {
		t.Fatal("no indexes written")
	}
	if err := os.WriteFile(indexes[0], []byte("AIRCIDX1 damaged"), 0o600); err != nil {
		t.Fatal(err)
	}
	s = archiveServer(t, path, 3, 2048, Config{})
	eventually(t, "the index rebuilt", func() bool { return len(s.archive.unindexed()) == 0 })
	if got := readAll(t, s, "#room", first.ID); len(got) != 100 {
		t.Fatalf("read %d messages after rebuilding the index", len(got))
	}
}

func TestArchivedMessagesKeepTheirCorrections(t *testing.T) {
	// Small segments seal the correction, so it comes back from the index;
	// a large one keeps it in the active file, read only on restore.
	for _, segment := range []int64{1024, 1 << 20} {
		t.Run(fmt.Sprint(segment), func(t *testing.T) { testArchivedCorrections(t, segment) })
	}
}

func testArchivedCorrections(t *testing.T, segment int64) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	s := archiveServer(t, path, 2, segment, Config{})
	before := post(s, "alice", "#room", "before")
	original := post(s, "alice", "#room", "original")
	s.messageMu.Lock()
	s.mu.Lock()
	fix := s.newMessage("alice", "#room", "corrected", &original)
	fix.Kind, fix.Supersedes = "correct", original.ID
	s.recordLocked(&fix)
	s.mu.Unlock()
	s.messageMu.Unlock()
	for i := range 50 {
		post(s, "alice", "#room", fmt.Sprint(i))
	}
	read := func(stage string) {
		s.mu.Lock()
		page, _, _, found := s.readAfterLocked("#room", before.ID, 1, nil)
		s.mu.Unlock()
		if !found || len(page) != 1 || page[0].ID != original.ID || page[0].SupersededBy != fix.ID || !page[0].Persisted {
			t.Fatalf("%s: archived original: %+v", stage, page)
		}
	}
	read("live")
	indexAll(t, s)
	s.Shutdown(context.Background())
	s = archiveServer(t, path, 2, segment, Config{})
	read("restarted")
}

func TestCheckAndHistoryResumeFromTheArchiveOverTheWire(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	address, s := startServer(t, Config{HistoryLimit: 4, PingInterval: time.Hour, ReadTimeout: time.Hour}, func(s *Server) error {
		s.histSegment = 2048
		return s.RestoreHistory(path)
	})
	var room []Message
	for i := range 60 {
		room = append(room, post(s, "alice", "#room", fmt.Sprint("m", i)))
	}
	conn, reader := registered(t, address, "reader")
	request, _ := json.Marshal(protocol.CheckRequest{Targets: []protocol.CheckTarget{{Target: "#room", After: room[10].ID, Limit: 5}}, MaxMessages: 100, IncludeOwn: true})
	io.WriteString(conn, "CHECK :"+string(request)+"\r\n")
	var bodies []string
	for {
		line := expectLine(t, reader, conn, " 78")
		if strings.Contains(line, " 785 ") {
			break
		}
		_, data, _ := strings.Cut(line, " :")
		var entry protocol.CheckEntry
		if json.Unmarshal([]byte(strings.TrimSpace(data)), &entry) != nil {
			continue
		}
		switch entry.Kind {
		case "message":
			bodies = append(bodies, entry.Message.Message)
		case "page":
			if entry.Gap || entry.Status != historyMore || entry.Cursor != room[15].ID {
				t.Fatalf("check page from the archive: %+v", entry)
			}
		}
	}
	if strings.Join(bodies, ",") != "m11,m12,m13,m14,m15" {
		t.Fatalf("check from the archive: %v", bodies)
	}
	io.WriteString(conn, "HISTORY #room 3 "+room[57].ID+"\r\n")
	end := expectLine(t, reader, conn, " 761 ")
	if !strings.Contains(end, " ok ") {
		t.Fatalf("history end: %s", end)
	}
	io.WriteString(conn, "HISTORY #room 3 "+room[20].ID+"\r\n")
	for _, want := range room[21:24] {
		if line := expectLine(t, reader, conn, " 760 "); !strings.Contains(line, `"id":"`+want.ID+`"`) {
			t.Fatalf("history from the archive: %s, want %s", line, want.Body)
		}
	}
	if end := expectLine(t, reader, conn, " 761 "); !strings.Contains(end, " more "+room[23].ID) {
		t.Fatalf("history end: %s", end)
	}
}
