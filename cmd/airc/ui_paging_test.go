package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/server"
)

func TestUIThreadAndSearchPagesSurviveReconnect(t *testing.T) {
	agentEnv(t)
	fastReconnect(t)
	first := startServerAt(t, "127.0.0.1:0", server.Config{HistoryLimit: 512}, filepath.Join(t.TempDir(), "history.jsonl"))
	address := first.addr
	root := send(t, address, "planner", "#room", "match 0")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := dialOneShot(ctx, options{nick: "planner", addr: address})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < 250; i++ {
		body := fmt.Sprintf("other %d", i)
		if i < 120 {
			body = fmt.Sprintf("match %d", i)
		}
		if err := c.Reply(root.ID, body); err != nil {
			t.Fatal(err)
		}
		if _, err := awaitSend(ctx, c, "planner", "", body, root.ID, "", "", nil); err != nil {
			t.Fatal(err)
		}
	}
	c.Close()
	h := startUIBackend(t, address, "#room")
	h.until("online", func() bool { return h.model.connected })
	h.cmds <- uiCmd{kind: "thread", target: root.ID}
	name := "thread:" + root.ID
	h.until("first thread page", func() bool { b := h.model.find(name); return b != nil && len(b.items) == 100 })
	if !strings.Contains(h.model.find(name).topic, "/next") {
		t.Fatal("continuation hidden")
	}
	first.stop()
	h.until("offline", func() bool { return !h.model.connected })
	first.restart(t, nil)
	h.until("reconnected", func() bool { return h.model.connected })
	for _, n := range []int{200, 250} {
		h.cmds <- uiCmd{kind: "query-next", target: name}
		h.until("next thread page", func() bool { return len(h.model.find(name).items) == n })
	}
	b := h.model.find(name)
	if len(b.seen) != 250 || b.query.Status != "ok" {
		t.Fatal("thread pages duplicated or incomplete", len(b.seen), b.query)
	}
	h.cmds <- uiCmd{kind: "search", target: "#room", text: "match"}
	search := "search:#room"
	h.until("first search page", func() bool { b := h.model.find(search); return b != nil && len(b.items) == 50 })
	for _, n := range []int{100, 120} {
		h.cmds <- uiCmd{kind: "query-next", target: search}
		h.until("next search page", func() bool { return len(h.model.find(search).items) == n })
	}
	if len(h.model.find(search).seen) != 120 || h.model.find(search).query.Status != "ok" {
		t.Fatal("search did not drain")
	}
	// Push its cursor out of the window: the archive continues the search,
	// with no gap and nothing repeated.
	evict(t, address, 520)
	h.cmds <- uiCmd{kind: "query-next", target: search}
	h.until("search continued", func() bool { return h.model.find(search).query.Status == "ok" && !h.model.find(search).query.Gap })
	if len(h.model.find(search).seen) != 120 {
		t.Fatal("search repeated or skipped messages after its cursor left the window")
	}
}

// Without a history file there is no archive, so an evicted search cursor
// is a gap, which the UI must keep visible.
func TestUISearchShowsARetentionGapWithoutAnArchive(t *testing.T) {
	agentEnv(t)
	address := cliTestServerWith(t, server.Config{HistoryLimit: 128})
	matches := make([]seedMessage, 60)
	for i := range matches {
		matches[i] = seedMessage{target: "#room", body: fmt.Sprint("match ", i)}
	}
	seedNativeMessages(t, address, "planner", matches)
	h := startUIBackend(t, address, "#room")
	h.until("online", func() bool { return h.model.connected })
	h.cmds <- uiCmd{kind: "search", target: "#room", text: "match"}
	search := "search:#room"
	h.until("first search page", func() bool { b := h.model.find(search); return b != nil && len(b.items) == 50 })
	evict(t, address, 200)
	h.cmds <- uiCmd{kind: "query-next", target: search}
	h.until("retention gap", func() bool { return h.model.find(search).query.Gap })
}

// evict posts n messages to another room, pushing older ones out of a window.
func evict(t *testing.T, address string, n int) {
	t.Helper()
	messages := make([]seedMessage, n)
	for i := range messages {
		messages[i] = seedMessage{target: "#other", body: fmt.Sprint(i)}
	}
	seedNativeMessages(t, address, "other", messages)
}
