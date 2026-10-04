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
	cfg := server.Config{HistoryLimit: 512}
	file := filepath.Join(t.TempDir(), "history.jsonl")
	first, address := startServerAt(t, "127.0.0.1:0", cfg, file)
	root := posted(t, address, "planner", "#room", "match 0")
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
	first.stop(t)
	h.until("offline", func() bool { return !h.model.connected })
	startServerAt(t, address, cfg, file)
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
	// Evict its cursor, then ask for more: the gap must stay visible.
	for i := range 520 {
		send(t, address, "other", "#other", fmt.Sprint(i))
	}
	h.cmds <- uiCmd{kind: "query-next", target: search}
	h.until("retention gap", func() bool { return h.model.find(search).query.Gap })
}
