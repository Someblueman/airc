package irc_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/pkg/irc"
)

func startConfigured(t *testing.T, cfg server.Config, historyFile string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg.PingInterval, cfg.ReadTimeout = time.Hour, time.Hour
	cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.New(cfg)
	if historyFile != "" {
		if err := srv.RestoreHistory(historyFile); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		<-done
	})
	return listener.Addr().String()
}

func dialOneShot(t *testing.T, nick, address string) *irc.Client {
	t.Helper()
	client, err := irc.Dial(irc.Config{Nick: nick, Addr: address, Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if !client.Ephemeral() {
		t.Fatal("server did not grant an ephemeral session")
	}
	return client
}

// sendOneShot sends from a fresh one-shot session and returns the stored message.
func sendOneShot(t *testing.T, nick, address, target, body string) *irc.SendReceiptEvent {
	t.Helper()
	client, err := irc.Dial(irc.Config{Nick: nick, Addr: address, Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Send(target, body); err != nil {
		t.Fatal(err)
	}
	return nextEvent(t, client, func(event irc.Event) bool {
		_, ok := event.(*irc.SendReceiptEvent)
		return ok
	}).(*irc.SendReceiptEvent)
}

func readHistory(t *testing.T, client *irc.Client, target, after string, limit int) ([]*irc.HistoryEvent, string) {
	t.Helper()
	if err := client.HistoryAfter(target, after, limit); err != nil {
		t.Fatal(err)
	}
	var messages []*irc.HistoryEvent
	for {
		switch event := nextEvent(t, client, func(event irc.Event) bool {
			switch event.(type) {
			case *irc.HistoryEvent, *irc.EndOfHistoryEvent:
				return true
			}
			return false
		}).(type) {
		case *irc.HistoryEvent:
			messages = append(messages, event)
		case *irc.EndOfHistoryEvent:
			return messages, event.Status
		}
	}
}

func bodies(messages []*irc.HistoryEvent) []string {
	out := make([]string, len(messages))
	for i, message := range messages {
		out[i] = message.Message
	}
	return out
}

func TestOneShotSessionsLeaveNoTraceAndDoNotClaimNicknames(t *testing.T) {
	address := startConfigured(t, server.Config{HistoryLimit: 32}, "")
	alice := newClient(t, "alice", address)
	if err := alice.Join("#room"); err != nil {
		t.Fatal(err)
	}
	nextEvent(t, alice, func(event irc.Event) bool { _, ok := event.(*irc.JoinEvent); return ok })

	// Same nickname as a live session: must not collide, join, or announce a quit.
	receipt := sendOneShot(t, "alice", address, "#room", "from a one-shot session")
	if receipt.Queued || receipt.ID == "" {
		t.Fatalf("unexpected receipt %#v", receipt)
	}
	nextEvent(t, alice, func(event irc.Event) bool {
		m, ok := event.(*irc.MessageEvent)
		return ok && m.Message == "from a one-shot session"
	})

	// A channel nobody is in still accepts and stores the message.
	sendOneShot(t, "planner", address, "#nobody-here", "left for later")
	reader := dialOneShot(t, "reader", address)
	if got, status := readHistory(t, reader, "#nobody-here", "", 10); len(got) != 1 || got[0].Message != "left for later" || status != "ok" {
		t.Fatalf("history for empty channel = %v %q", bodies(got), status)
	}

	// Marker proves ordering: nothing but messages reached alice in between.
	if err := alice.Send("#room", "marker"); err != nil {
		t.Fatal(err)
	}
	for {
		event := nextEvent(t, alice, func(irc.Event) bool { return true })
		switch e := event.(type) {
		case *irc.MessageEvent:
			if e.Message == "marker" {
				goto checked
			}
		case *irc.JoinEvent, *irc.PartEvent, *irc.QuitEvent:
			t.Fatalf("one-shot session leaked presence event %#v", e)
		}
	}
checked:
	// The one-shot sessions are not listed, and alice still owns her nickname.
	if err := alice.Raw("AGENTS"); err != nil {
		t.Fatal(err)
	}
	for {
		event := nextEvent(t, alice, func(event irc.Event) bool {
			_, agent := event.(*irc.AgentsEvent)
			_, end := event.(*irc.EndOfAgentsEvent)
			return agent || end
		})
		if agent, ok := event.(*irc.AgentsEvent); ok {
			t.Fatalf("one-shot session listed as agent: %#v", agent)
		}
		break
	}
	if _, err := irc.Dial(irc.Config{Nick: "alice", Addr: address}); err == nil {
		t.Fatal("alice's nickname was released by a one-shot session")
	}
}

func TestOfflineDirectMessagesAreQueuedAndReadable(t *testing.T) {
	address := startConfigured(t, server.Config{HistoryLimit: 32}, "")
	queued := sendOneShot(t, "planner", address, "builder", "please build task 7")
	if !queued.Queued {
		t.Fatal("message to an offline nick should be reported as queued")
	}
	reader := dialOneShot(t, "builder", address)
	got, status := readHistory(t, reader, "Builder", "", 10) // nicknames are case-insensitive
	if len(got) != 1 || got[0].Message != "please build task 7" || got[0].From != "planner" || status != "ok" {
		t.Fatalf("inbox = %v %q", bodies(got), status)
	}

	// A connected recipient gets it live and the receipt is not flagged queued.
	live := newClient(t, "reviewer", address)
	if receipt := sendOneShot(t, "planner", address, "reviewer", "live one"); receipt.Queued {
		t.Fatal("message to a connected nick must not be flagged queued")
	}
	nextEvent(t, live, func(event irc.Event) bool {
		m, ok := event.(*irc.MessageEvent)
		return ok && m.Message == "live one"
	})
	// Live delivery is also stored, so a later inbox read sees it too.
	if got, _ := readHistory(t, reader, "reviewer", "", 10); len(got) != 1 {
		t.Fatalf("live direct message missing from history: %v", bodies(got))
	}
}

func TestDirectMessageToUnknownNickStillFailsWithoutHistory(t *testing.T) {
	address := startConfigured(t, server.Config{}, "")
	client := dialOneShot(t, "planner", address)
	if err := client.Send("nobody", "hello?"); err != nil {
		t.Fatal(err)
	}
	nextEvent(t, client, func(event irc.Event) bool {
		m, ok := event.(*irc.RawEvent)
		return ok && m.Command == "401"
	})
}

func TestHistoryCursorPagesForwardAndReportsExpiry(t *testing.T) {
	address := startConfigured(t, server.Config{HistoryLimit: 12}, "")
	var first *irc.SendReceiptEvent
	for i := 1; i <= 5; i++ {
		receipt := sendOneShot(t, "writer", address, "#log", fmt.Sprintf("log %d", i))
		if first == nil {
			first = receipt
		}
		sendOneShot(t, "writer", address, "#other", "unrelated") // must never show up in #log
	}
	reader := dialOneShot(t, "reader", address)

	page, status := readHistory(t, reader, "#log", first.ID, 2)
	if fmt.Sprint(bodies(page)) != "[log 2 log 3]" || status != "more" {
		t.Fatalf("page 1 = %v %q", bodies(page), status)
	}
	page, status = readHistory(t, reader, "#log", page[len(page)-1].ID, 10)
	if fmt.Sprint(bodies(page)) != "[log 4 log 5]" || status != "ok" {
		t.Fatalf("page 2 = %v %q", bodies(page), status)
	}
	if page[0].Seq == 0 || page[1].Seq <= page[0].Seq {
		t.Fatalf("messages lack increasing sequence numbers: %d %d", page[0].Seq, page[1].Seq)
	}
	if caught, status := readHistory(t, reader, "#log", page[len(page)-1].ID, 10); len(caught) != 0 || status != "ok" {
		t.Fatalf("caught-up read = %v %q", bodies(caught), status)
	}

	// The window holds 12 messages and 10 are stored; 5 more push the first cursor out.
	for i := 0; i < 5; i++ {
		sendOneShot(t, "writer", address, "#log", fmt.Sprintf("later %d", i))
	}
	got, status := readHistory(t, reader, "#log", first.ID, 10)
	if status != "expired" || len(got) == 0 || got[len(got)-1].Message != "later 4" {
		t.Fatalf("expired cursor = %v %q", bodies(got), status)
	}
}

func TestObserveDirectMessagesByNickname(t *testing.T) {
	address := startConfigured(t, server.Config{HistoryLimit: 8}, "")
	watcher := dialOneShot(t, "carol", address)
	if err := watcher.Observe("#news", "@Carol"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		nextEvent(t, watcher, func(event irc.Event) bool {
			r, ok := event.(*irc.RawEvent)
			return ok && r.Command == "765"
		})
	}
	sendOneShot(t, "dave", address, "carol", "psst")
	sendOneShot(t, "dave", address, "erin", "not for carol")
	sendOneShot(t, "dave", address, "#news", "headline")
	var seen []string
	for len(seen) < 2 {
		m := nextEvent(t, watcher, func(event irc.Event) bool { _, ok := event.(*irc.MessageEvent); return ok }).(*irc.MessageEvent)
		seen = append(seen, m.Message)
	}
	if fmt.Sprint(seen) != "[psst headline]" {
		t.Fatalf("observer saw %v", seen)
	}
	// An observing one-shot session can still query history on the same connection.
	if got, _ := readHistory(t, watcher, "carol", "", 10); len(got) != 1 {
		t.Fatalf("history while observing = %v", bodies(got))
	}
}

func TestHistorySurvivesServerRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := server.Config{HistoryLimit: 8, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	first := server.New(cfg)
	if err := first.RestoreHistory(path); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- first.Serve(listener) }()
	address := listener.Addr().String()
	before := sendOneShot(t, "planner", address, "#ops", "deploy at noon")
	queued := sendOneShot(t, "planner", address, "builder", "queued across restart")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := first.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	<-done

	address = startConfigured(t, server.Config{HistoryLimit: 8}, path)
	reader := dialOneShot(t, "reader", address)
	got, status := readHistory(t, reader, "#ops", before.ID, 10)
	if len(got) != 0 || status != "ok" {
		t.Fatalf("a cursor from before the restart must still resolve: %v %q", bodies(got), status)
	}
	if got, _ := readHistory(t, reader, "builder", "", 10); len(got) != 1 || got[0].Message != "queued across restart" {
		t.Fatalf("queued direct message lost over restart: %v", bodies(got))
	}
	after := sendOneShot(t, "planner", address, "#ops", "restarted")
	if after.Seq <= queued.Seq {
		t.Fatalf("sequence went backwards across restart: %d then %d", queued.Seq, after.Seq)
	}
}
