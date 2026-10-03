package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestUIStateKeepsPerViewDraftsAndRejectsConcurrentWriters(t *testing.T) {
	agentEnv(t)
	opt := options{nick: "me", addr: "127.0.0.1:6667"}
	m := newTestModel("#a", "#b")
	s, err := openUIState(opt, m)
	if err != nil {
		t.Fatal(err)
	}
	m.input = []rune("draft é🐈\nline")
	m.cursor = 3
	m.replyTo = strings.Repeat("a", 32)
	m.switchTo("#b")
	m.input = []rune("other")
	m.cursor = 5
	if err := s.save(m); err != nil {
		t.Fatal(err)
	}
	if second, err := openUIState(opt, newTestModel("#a")); err == nil {
		second.lock.Close()
		t.Fatal("concurrent writer accepted")
	}
	s.lock.Close()
	next := newTestModel("#a")
	s, err = openUIState(opt, next)
	if err != nil {
		t.Fatal(err)
	}
	defer s.lock.Close()
	if next.current != "#b" || string(next.input) != "other" {
		t.Fatal("active draft not restored")
	}
	next.switchTo("#a")
	if string(next.input) != "draft é🐈\nline" || next.cursor != 3 || next.replyTo != strings.Repeat("a", 32) {
		t.Fatal("per-view draft changed")
	}
	info, _ := os.Stat(s.path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("draft file permissions", info.Mode())
	}
}

func TestUIRestartRecoversAcceptedSendWithoutModelConfirmation(t *testing.T) {
	address := chatServer(t, 64)
	opt := options{nick: "me", addr: address}
	m := newTestModel("#room")
	m.connected = true
	s, err := openUIState(opt, m)
	if err != nil {
		t.Fatal(err)
	}
	cmds, _ := typeText(m, "durable UI message")
	if err := prepareUIDelivery(opt, m, cmds); err != nil {
		t.Fatal(err)
	}
	if err := s.save(m); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := dialOneShot(ctx, opt)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	b := uiBackend{opt: opt, nick: "me"}
	if _, err, _ := b.sendConfirmed(ctx, c, cmds[0], nil); err != nil {
		t.Fatal(err)
	}
	// Discard the old process model before it sees acceptance. Its on-disk UI
	// record still says uncertain, while the shared outbox has the saved receipt.
	s.lock.Close()
	next := newTestModel("#room")
	next.connected = true
	s, err = openUIState(opt, next)
	if err != nil {
		t.Fatal(err)
	}
	defer s.lock.Close()
	if next.uncertainID == "" || string(next.input) != "durable UI message" {
		t.Fatal("recovery state lost")
	}
	cmds, _ = next.update(keyIn{kind: keyEnter})
	if len(cmds) != 1 || cmds[0].kind != "retry-send" {
		t.Fatal("restart would repost", cmds)
	}
	status, err, id := b.sendConfirmed(ctx, c, cmds[0], nil)
	next.delivery(deliveryIn{cmd: cmds[0], err: err, uncertainID: id, acceptance: status})
	if err != nil || next.uncertainID != "" || len(next.input) != 0 {
		t.Fatal("receipt recovery failed", err)
	}
	if ids := historyIDs(t, mustCLI(t, address, "history", "#room", "--json")); len(ids) != 1 {
		t.Fatal("duplicate post", ids)
	}
}

func TestUIUnconfirmedChatRestoresWithoutAutomaticReplay(t *testing.T) {
	agentEnv(t)
	opt := options{nick: "me", addr: "127.0.0.1:6667"}
	m := newTestModel("#room")
	m.connected = true
	s, err := openUIState(opt, m)
	if err != nil {
		t.Fatal(err)
	}
	cmds, _ := typeText(m, "/me checking this")
	if len(cmds) != 1 || !uncertainChat(m.pending) {
		t.Fatal("action not tracked")
	}
	if err := s.save(m); err != nil {
		t.Fatal(err)
	}
	s.lock.Close()
	next := newTestModel("#room")
	next.connected = true
	s, err = openUIState(opt, next)
	if err != nil {
		t.Fatal(err)
	}
	defer s.lock.Close()
	if cmds, _ := next.update(keyIn{kind: keyEnter}); len(cmds) != 0 || !next.uncertainAction || string(next.input) != "/me checking this" {
		t.Fatal("uncertain action replayed or lost")
	}
	next.update(keyIn{kind: keyEsc})
	if next.uncertainAction || len(next.input) != 0 {
		t.Fatal("could not discard uncertain action")
	}
	queue := make(chan uiCmd, 1)
	queue <- uiCmd{kind: "channels"}
	queueUICommands(m, queue, cmds, string(m.input), m.cursor)
	if m.uncertainAction || m.pending != nil {
		t.Fatal("busy queue marked an unattempted action uncertain")
	}
}
