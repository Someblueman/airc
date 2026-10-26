package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// pasteServer starts a server with history and pastes persisted under dir,
// so a second call with the same dir restores them. skew moves its clock.
func pasteServer(t *testing.T, dir string, cfg Config, skew *atomic.Int64) (string, *Server) {
	t.Helper()
	cfg.PingInterval, cfg.ReadTimeout = time.Hour, time.Hour
	if cfg.HistoryLimit == 0 {
		cfg.HistoryLimit = 64
	}
	if skew != nil {
		cfg.Now = func() time.Time { return time.Now().Add(time.Duration(skew.Load())) }
	}
	return startServer(t, cfg, func(s *Server) error {
		if err := s.RestoreHistory(filepath.Join(dir, "history")); err != nil {
			return err
		}
		return s.RestorePastes(filepath.Join(dir, "pastes.json"))
	})
}

func pasteContent(prefix string, size int) []byte {
	var b bytes.Buffer
	for i := 0; b.Len() < size; i++ {
		b.WriteString(prefix)
		b.WriteString(strings.Repeat("x", 40))
		b.WriteByte('\n')
	}
	return b.Bytes()[:size]
}

func mustUpload(t *testing.T, c *irc.Client, name string, content []byte) irc.PasteInfo {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	info, err := irc.UploadPaste(ctx, c, name, "text", content, nil)
	if err != nil {
		t.Fatalf("upload %s: %v", name, err)
	}
	return info
}

func mustFetch(t *testing.T, c *irc.Client, id string) (irc.ChatEntry, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	meta, content, err := irc.FetchPaste(ctx, c, id, nil)
	if err != nil {
		t.Fatalf("fetch %s: %v", id, err)
	}
	return meta, content
}

// sharePaste posts body sharing paste id in room and returns the message as
// delivered back to c.
func sharePaste(t *testing.T, c *irc.Client, room, body, id string) *irc.MessageEvent {
	t.Helper()
	if err := c.SendPaste(room, body, "", id); err != nil {
		t.Fatal(err)
	}
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e := <-c.Events():
			if m, ok := e.(*irc.MessageEvent); ok && m.Paste != nil && m.Paste.ID == id {
				return m
			}
		case <-timeout:
			t.Fatalf("message sharing %s was not delivered", id)
		}
	}
}

// pasteIDs lists the pastes the server holds.
func pasteIDs(s *Server) map[string]pasteRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]pasteRecord{}
	for id, r := range s.pastes {
		out[id] = r
	}
	return out
}

// An upload travels in chunks, is stored once however often it is uploaded,
// reads back byte for byte, and is listed for its author and described to
// anyone by ID.
func TestPasteUploadDedupesByContentAndReadsBack(t *testing.T) {
	dir := t.TempDir()
	address, s := pasteServer(t, dir, Config{}, nil)
	author := handoffClient(t, address, "author", "#room")
	reader := handoffClient(t, address, "reader", "#room")
	content := pasteContent("line ", 20<<10)
	info := mustUpload(t, author, "test.log", content)
	if info.ID != protocol.PasteID(content) || info.Size != len(content) || info.Lines != protocol.PasteLines(content) || info.Name != "test.log" || info.Language != "text" {
		t.Fatalf("paste %+v", info)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "pastes", info.ID)); err != nil || !bytes.Equal(data, content) {
		t.Fatalf("stored file: %v", err)
	}
	again := mustUpload(t, reader, "copy.log", content)
	if again.ID != info.ID || again.Name != "test.log" {
		t.Fatalf("a second upload of the same content made %+v", again)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "pastes"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("files after a duplicate upload: %d, %v", len(entries), err)
	}
	meta, got := mustFetch(t, reader, info.ID)
	if !bytes.Equal(got, content) || meta.From != "author" || meta.Paste == nil || meta.Paste.Size != len(content) || meta.Created.IsZero() {
		t.Fatalf("fetched %d bytes, meta %+v", len(got), meta)
	}
	mine, _, err := handoffChat(t, author, protocol.ChatRequest{Action: "pastes"})
	if err != nil || len(mine) != 1 || mine[0].ID != info.ID || mine[0].Action != "paste" {
		t.Fatalf("author's pastes %+v, %v", mine, err)
	}
	if others, _, err := handoffChat(t, reader, protocol.ChatRequest{Action: "pastes"}); err != nil || len(others) != 0 {
		t.Fatalf("reader's pastes %+v, %v", others, err)
	}
	one, _, err := handoffChat(t, reader, protocol.ChatRequest{Action: "pastes", ID: info.ID})
	if err != nil || len(one) != 1 || one[0].Paste == nil || one[0].Paste.Lines != info.Lines {
		t.Fatalf("paste by id %+v, %v", one, err)
	}
	if _, _, err := handoffChat(t, reader, protocol.ChatRequest{Action: "paste-get", ID: strings.Repeat("0", 32)}); err == nil || !strings.Contains(err.Error(), "not retained") {
		t.Fatalf("unknown paste: %v", err)
	}
	// A page is bounded, and reading from an offset continues it.
	page, _, err := handoffChat(t, reader, protocol.ChatRequest{Action: "paste-get", ID: info.ID, Offset: 4096, Limit: 2})
	if err != nil || len(page) != 2 || page[0].Offset != 4096 || page[0].Paste == nil || page[1].Offset != 8192 || page[1].Paste != nil {
		t.Fatalf("page %+v, %v", page, err)
	}
	if chunk, _ := base64.StdEncoding.DecodeString(page[1].Data); !bytes.Equal(chunk, content[8192:12288]) {
		t.Fatal("page data")
	}
	s.mu.Lock()
	total := s.total
	s.mu.Unlock()
	if total != int64(len(content)) {
		t.Fatalf("total %d", total)
	}
}

// A paste over the size limit, binary content, a chunk out of order and an
// unfinished upload are refused; the total limit evicts the oldest paste no
// retained message shares, and refuses an upload once only shared pastes
// remain.
func TestPasteLimitsBinaryAndEviction(t *testing.T) {
	dir := t.TempDir()
	address, s := pasteServer(t, dir, Config{PasteBytes: 1024, PasteTotalBytes: 2048}, nil)
	c := handoffClient(t, address, "author", "#room")
	ctx := t.Context()
	if _, err := irc.UploadPaste(ctx, c, "big.txt", "", pasteContent("", 1025), nil); err == nil || !strings.Contains(err.Error(), "at most 1024") {
		t.Fatalf("oversized: %v", err)
	}
	begin := func(token string, size int) {
		t.Helper()
		if _, _, err := handoffChat(t, c, protocol.ChatRequest{Action: "paste-begin", Token: token, Name: "x", Size: size}); err != nil {
			t.Fatalf("begin %s: %v", token, err)
		}
	}
	if _, _, err := handoffChat(t, c, protocol.ChatRequest{Action: "paste-begin", Token: "big", Name: "x", Size: 1025}); err == nil || !strings.Contains(err.Error(), "1 to 1024 bytes") {
		t.Fatalf("server-side size check: %v", err)
	}
	begin("bin", 4)
	if _, _, err := handoffChat(t, c, protocol.ChatRequest{Action: "paste-chunk", Token: "bin", Seq: 0, Data: base64.StdEncoding.EncodeToString([]byte("a\x00bc"))}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := handoffChat(t, c, protocol.ChatRequest{Action: "paste-end", Token: "bin"}); err == nil || !strings.Contains(err.Error(), "binary is refused") {
		t.Fatalf("binary: %v", err)
	}
	begin("order", 8)
	if _, _, err := handoffChat(t, c, protocol.ChatRequest{Action: "paste-chunk", Token: "order", Seq: 1, Data: base64.StdEncoding.EncodeToString([]byte("abcd"))}); err == nil || !strings.Contains(err.Error(), "chunk 0 expected") {
		t.Fatalf("out of order: %v", err)
	}
	if _, _, err := handoffChat(t, c, protocol.ChatRequest{Action: "paste-end", Token: "order"}); err == nil || !strings.Contains(err.Error(), "0 of its declared 8") {
		t.Fatalf("short upload: %v", err)
	}
	if _, _, err := handoffChat(t, c, protocol.ChatRequest{Action: "paste-end", Token: "order"}); err == nil || !strings.Contains(err.Error(), "unknown upload token") {
		t.Fatalf("ended upload: %v", err)
	}
	for _, token := range []string{"a", "b", "c", "d"} {
		begin(token, 4)
	}
	if _, _, err := handoffChat(t, c, protocol.ChatRequest{Action: "paste-begin", Token: "e", Name: "x", Size: 4}); err == nil || !strings.Contains(err.Error(), "4 uploads are in flight") {
		t.Fatalf("fifth upload: %v", err)
	}
	for _, token := range []string{"a", "b", "c", "d"} {
		// Ending an empty upload fails and frees its token.
		if _, _, err := handoffChat(t, c, protocol.ChatRequest{Action: "paste-end", Token: token}); err == nil {
			t.Fatalf("empty upload %s was stored", token)
		}
	}

	first := mustUpload(t, c, "first.txt", pasteContent("1", 1000))
	second := mustUpload(t, c, "second.txt", pasteContent("2", 1000))
	third := mustUpload(t, c, "third.txt", pasteContent("3", 1000))
	held := pasteIDs(s)
	if _, kept := held[first.ID]; kept || len(held) != 2 {
		t.Fatalf("after the total was exceeded: %v", held)
	}
	if _, err := os.Stat(filepath.Join(dir, "pastes", first.ID)); !os.IsNotExist(err) {
		t.Fatalf("evicted file remains: %v", err)
	}
	sharePaste(t, c, "#room", protocol.PasteMessageBody("see", second), second.ID)
	sharePaste(t, c, "#room", protocol.PasteMessageBody("and", third), third.ID)
	if _, err := irc.UploadPaste(ctx, c, "fourth.txt", "", pasteContent("4", 1000), nil); err == nil || !strings.Contains(err.Error(), "full") {
		t.Fatalf("a store full of shared pastes: %v", err)
	}
	if held := pasteIDs(s); len(held) != 2 {
		t.Fatalf("a refused upload changed the store: %v", held)
	}
}

// An upload not finished within a minute is discarded, so its token is
// unknown afterwards, and a dropped connection takes its uploads with it.
func TestPasteUploadIsDiscardedWhenUnfinished(t *testing.T) {
	var skew atomic.Int64
	address, s := pasteServer(t, t.TempDir(), Config{}, &skew)
	c := handoffClient(t, address, "author", "#room")
	if _, _, err := handoffChat(t, c, protocol.ChatRequest{Action: "paste-begin", Token: "slow", Name: "slow.txt", Size: 5}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := handoffChat(t, c, protocol.ChatRequest{Action: "paste-chunk", Token: "slow", Seq: 0, Data: base64.StdEncoding.EncodeToString([]byte("hello"))}); err != nil {
		t.Fatal(err)
	}
	skew.Store(int64(61 * time.Second))
	if _, _, err := handoffChat(t, c, protocol.ChatRequest{Action: "paste-end", Token: "slow"}); err == nil || !strings.Contains(err.Error(), "unknown upload token") {
		t.Fatalf("stale upload: %v", err)
	}
	if held := pasteIDs(s); len(held) != 0 {
		t.Fatalf("a discarded upload was stored: %v", held)
	}
	// The token is free again, and a new upload under it completes.
	if _, _, err := handoffChat(t, c, protocol.ChatRequest{Action: "paste-begin", Token: "slow", Name: "slow.txt", Size: 5}); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	inFlight := 0
	for _, client := range s.clients {
		inFlight += len(client.uploads)
	}
	s.mu.Unlock()
	if inFlight != 1 {
		t.Fatalf("%d uploads in flight", inFlight)
	}
	_ = c.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.Lock()
		gone := len(s.clients) == 0
		s.mu.Unlock()
		if gone || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.mu.Lock()
	remaining := len(s.clients)
	s.mu.Unlock()
	if remaining != 0 {
		t.Fatal("the connection was not removed")
	}
}

// A paste is dropped once its retention has passed since it was last shared
// or read, unless a message sharing it is still retained; a read refreshes
// it; and once the sharing message leaves the window the paste goes too.
func TestPasteRetentionKeepsSharedPastes(t *testing.T) {
	var skew atomic.Int64
	dir := t.TempDir()
	address, s := pasteServer(t, dir, Config{HistoryLimit: 4, PasteRetain: time.Hour}, &skew)
	c := handoffClient(t, address, "author", "#room")
	shared := mustUpload(t, c, "shared.txt", pasteContent("s", 300))
	read := mustUpload(t, c, "read.txt", pasteContent("r", 300))
	stale := mustUpload(t, c, "stale.txt", pasteContent("x", 300))
	sharePaste(t, c, "#room", protocol.PasteMessageBody("look", shared), shared.ID)
	skew.Store(int64(50 * time.Minute))
	mustFetch(t, c, read.ID) // a read is a reference
	skew.Store(int64(90 * time.Minute))
	sweep := func() {
		s.mu.Lock()
		s.sweepPastesLocked(s.now())
		s.mu.Unlock()
	}
	sweep()
	held := pasteIDs(s)
	if _, ok := held[stale.ID]; ok {
		t.Fatal("an unreferenced paste survived its retention")
	}
	if _, ok := held[read.ID]; !ok {
		t.Fatal("a read did not count as a reference")
	}
	if r, ok := held[shared.ID]; !ok || len(r.Messages) != 1 {
		t.Fatalf("a shared paste was dropped while its message is retained: %+v", r)
	}
	// Once the sharing message has left the history window, nothing holds it.
	for i := range 5 {
		if err := c.Send("#room", "filler "+strings.Repeat("y", i)); err != nil {
			t.Fatal(err)
		}
	}
	for {
		m := nextMessage(t, c, "")
		if m.Message == "filler yyyy" {
			break
		}
	}
	skew.Store(int64(3 * time.Hour))
	sweep()
	if held := pasteIDs(s); len(held) != 0 {
		t.Fatalf("after the sharing message left history and the read aged: %v", held)
	}
	if _, err := os.ReadDir(filepath.Join(dir, "pastes")); err != nil {
		t.Fatal(err)
	}
}

// The index and the files survive a restart; an indexed paste whose file is
// missing is dropped from the index with a warning, and a file the index
// does not know is removed.
func TestPasteRestoreSurvivesRestartAndMissingFiles(t *testing.T) {
	dir := t.TempDir()
	address, first := pasteServer(t, dir, Config{}, nil)
	c := handoffClient(t, address, "author", "#room")
	kept := mustUpload(t, c, "kept.txt", pasteContent("k", 5000))
	lost := mustUpload(t, c, "lost.txt", pasteContent("l", 700))
	shared := sharePaste(t, c, "#room", protocol.PasteMessageBody("keep this", kept), kept.ID)
	_ = c.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := first.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	t.Run("restart", func(t *testing.T) {
		if err := os.Remove(filepath.Join(dir, "pastes", lost.ID)); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "pastes", strings.Repeat("f", 32)), []byte("stray"), 0o600); err != nil {
			t.Fatal(err)
		}
		address, s := pasteServer(t, dir, Config{}, nil)
		held := pasteIDs(s)
		if _, ok := held[lost.ID]; ok || len(held) != 1 {
			t.Fatalf("restored index %v", held)
		}
		if r := held[kept.ID]; r.Name != "kept.txt" || r.Author != "author" || len(r.Messages) != 1 || r.Messages[0] != shared.ID {
			t.Fatalf("restored record %+v", r)
		}
		if _, err := os.Stat(filepath.Join(dir, "pastes", strings.Repeat("f", 32))); !os.IsNotExist(err) {
			t.Fatal("a stray file was kept")
		}
		data, err := os.ReadFile(filepath.Join(dir, "pastes.json"))
		if err != nil || strings.Contains(string(data), lost.ID) || !strings.Contains(string(data), kept.ID) {
			t.Fatalf("rewritten index: %v %s", err, data)
		}
		reader := handoffClient(t, address, "reader", "#room")
		if _, content := mustFetch(t, reader, kept.ID); !bytes.Equal(content, pasteContent("k", 5000)) {
			t.Fatal("content changed across restart")
		}
		// A file that vanishes while the server runs is dropped on read.
		if err := os.Remove(filepath.Join(dir, "pastes", kept.ID)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := handoffChat(t, reader, protocol.ChatRequest{Action: "paste-get", ID: kept.ID}); err == nil || !strings.Contains(err.Error(), "missing") {
			t.Fatalf("vanished file: %v", err)
		}
		if held := pasteIDs(s); len(held) != 0 {
			t.Fatalf("a paste without its file stayed indexed: %v", held)
		}
	})
}
