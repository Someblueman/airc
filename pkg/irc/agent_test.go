package irc_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
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

// joinAndWait joins a channel and waits for the server to finish the join, so a
// later send from another connection cannot race ahead of it.
func joinAndWait(t *testing.T, c *irc.Client, channel string) {
	t.Helper()
	if err := c.Join(channel); err != nil {
		t.Fatal(err)
	}
	nextEvent(t, c, func(event irc.Event) bool {
		r, ok := event.(*irc.RawEvent)
		return ok && r.Command == "366"
	})
}

func TestMultilineMessagesRoundTripAndDegradeForPlainClients(t *testing.T) {
	address := startConfigured(t, server.Config{HistoryLimit: 16}, "")
	alice := newClient(t, "alice", address)
	bob := newClient(t, "bob", address)
	if !alice.Multiline() {
		t.Fatal("server did not advertise multi-line support")
	}
	joinAndWait(t, alice, "#room")
	joinAndWait(t, bob, "#room")
	// A plain IRC client (nc, irssi) knows nothing about the tag.
	plain, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	io.WriteString(plain, "NICK plain\r\nUSER p 0 * :p\r\nJOIN #room\r\n")
	reader := bufio.NewReader(plain)
	plain.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("plain client never finished joining: %v", err)
		}
		if strings.Contains(line, " 366 ") {
			break
		}
	}

	body := "Status report\n\n- built: yes\n- tests: passing ✓\r\nDone."
	want := "Status report\n\n- built: yes\n- tests: passing ✓\nDone."
	if err := alice.Send("#room", body); err != nil {
		t.Fatal(err)
	}
	got := nextEvent(t, bob, func(event irc.Event) bool {
		m, ok := event.(*irc.MessageEvent)
		return ok && m.From == "alice"
	}).(*irc.MessageEvent)
	if got.Message != want {
		t.Fatalf("bob received %q, want %q", got.Message, want)
	}
	echo := nextEvent(t, alice, func(event irc.Event) bool {
		m, ok := event.(*irc.MessageEvent)
		return ok && m.From == "alice"
	}).(*irc.MessageEvent)
	if echo.Message != want {
		t.Fatalf("sender echo = %q", echo.Message)
	}

	plain.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("plain client never saw the message: %v", err)
		}
		if strings.Contains(line, "PRIVMSG #room") {
			if !strings.HasSuffix(line, ":Status report ⏎ - built: yes ⏎ - tests: passing ✓ ⏎ Done.\r\n") {
				t.Fatalf("plain client line = %q", line)
			}
			break
		}
	}

	reader2 := dialOneShot(t, "reader", address)
	if msgs, _ := readHistory(t, reader2, "#room", "", 5); len(msgs) != 1 || msgs[0].Message != want {
		t.Fatalf("history body = %v", bodies(msgs))
	}
	// Direct messages queue with their line breaks intact too.
	if receipt := sendOneShot(t, "alice", address, "carol", "a\nb"); !receipt.Queued || receipt.Message != "a\nb" {
		t.Fatalf("queued multi-line receipt = %#v", receipt)
	}
	if msgs, _ := readHistory(t, reader2, "carol", "", 5); len(msgs) != 1 || msgs[0].Message != "a\nb" {
		t.Fatalf("inbox body = %v", bodies(msgs))
	}
}

func TestMultilineBodiesCannotInjectProtocolCommands(t *testing.T) {
	address := startConfigured(t, server.Config{HistoryLimit: 16}, "")
	alice := newClient(t, "alice", address)
	bob := newClient(t, "bob", address)
	joinAndWait(t, alice, "#room")
	joinAndWait(t, bob, "#room")
	attack := "hi\r\nQUIT :gone\r\nPRIVMSG #room :forged\nJOIN #elsewhere"
	if err := alice.Send("#room", attack); err != nil {
		t.Fatal(err)
	}
	got := nextEvent(t, bob, func(event irc.Event) bool { _, ok := event.(*irc.MessageEvent); return ok }).(*irc.MessageEvent)
	if got.Message != "hi\nQUIT :gone\nPRIVMSG #room :forged\nJOIN #elsewhere" || got.From != "alice" {
		t.Fatalf("body was not delivered as plain text: %#v", got)
	}
	// alice is still connected and in the room: the text was never run as commands.
	if err := alice.Send("#room", "still here"); err != nil {
		t.Fatal(err)
	}
	next := nextEvent(t, bob, func(event irc.Event) bool { _, ok := event.(*irc.MessageEvent); return ok }).(*irc.MessageEvent)
	if next.Message != "still here" {
		t.Fatalf("unexpected follow-up %#v", next)
	}
}

func TestClientRefusesMultilineWhenServerDoesNotAdvertiseIt(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if strings.HasPrefix(line, "USER") {
				io.WriteString(conn, ":server 001 old :Welcome\r\n")
			}
		}
	}()
	client, err := irc.Dial(irc.Config{Nick: "old", Addr: listener.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if client.Multiline() {
		t.Fatal("client assumed multi-line support")
	}
	if err := client.Send("#room", "a\nb"); err == nil || !strings.Contains(err.Error(), "multi-line") {
		t.Fatalf("multi-line send to an old server = %v; it must fail rather than flatten the message", err)
	}
	if err := client.Send("#room", "single line"); err != nil {
		t.Fatalf("single-line send regressed: %v", err)
	}
}

func TestMentionInboxCollectsTagsAcrossChannelsAndDirectMessages(t *testing.T) {
	address := startConfigured(t, server.Config{HistoryLimit: 32}, "")
	sendOneShot(t, "planner", address, "#alpha", "hey @Bob, take task 7")
	sendOneShot(t, "planner", address, "#alpha", "unrelated chatter")
	sendOneShot(t, "planner", address, "#beta", "bob: please also review this")
	sendOneShot(t, "planner", address, "#beta", "email bob@example.com is not a tag")
	sendOneShot(t, "bob", address, "#alpha", "@bob tagging myself must not count")
	sendOneShot(t, "planner", address, "bob", "a direct message")
	sendOneShot(t, "planner", address, "#alpha", "@carol this one is for carol")

	reader := dialOneShot(t, "bob", address)
	if !reader.Supports("MENTIONS") || !reader.Supports("multiline") || reader.Supports("NOPE") {
		t.Fatal("server did not advertise its features")
	}
	got, status := readHistory(t, reader, "@bob", "", 10)
	want := "[hey @Bob, take task 7 bob: please also review this a direct message]"
	if fmt.Sprint(bodies(got)) != want || status != "ok" {
		t.Fatalf("mention inbox = %v %q, want %s", bodies(got), status, want)
	}
	// The inbox pages with a cursor like any other target.
	rest, _ := readHistory(t, reader, "@bob", got[0].ID, 10)
	if fmt.Sprint(bodies(rest)) != "[bob: please also review this a direct message]" {
		t.Fatalf("inbox after cursor = %v", bodies(rest))
	}
	// A bare nick is unchanged: direct messages only.
	if dms, _ := readHistory(t, reader, "bob", "", 10); fmt.Sprint(bodies(dms)) != "[a direct message]" {
		t.Fatalf("bare-nick inbox = %v", bodies(dms))
	}
}

func TestObserversOfANickAreWokenByTagsInAnyChannel(t *testing.T) {
	address := startConfigured(t, server.Config{HistoryLimit: 32}, "")
	watcher := dialOneShot(t, "dana", address)
	if err := watcher.Observe("@Dana"); err != nil {
		t.Fatal(err)
	}
	nextEvent(t, watcher, func(event irc.Event) bool {
		r, ok := event.(*irc.RawEvent)
		return ok && r.Command == "765"
	})
	// Nobody is in these channels and the watcher follows neither.
	sendOneShot(t, "erin", address, "#elsewhere", "nothing for dana here")
	sendOneShot(t, "dana", address, "#elsewhere", "@dana self tag is ignored")
	sendOneShot(t, "erin", address, "#elsewhere", "psst @dana look")
	m := nextEvent(t, watcher, func(event irc.Event) bool { _, ok := event.(*irc.MessageEvent); return ok }).(*irc.MessageEvent)
	if m.Message != "psst @dana look" || m.Target != "#elsewhere" || !m.Mentions("DANA") {
		t.Fatalf("watcher was woken by %#v", m)
	}
}
