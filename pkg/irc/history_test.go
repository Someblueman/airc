package irc_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestOneShotSessionsLeaveNoTraceAndDoNotClaimNicknames(t *testing.T) {
	address := startServer(t, server.Config{HistoryLimit: 32}, nil)
	alice := newClient(t, "alice", address)
	if err := alice.Join("#room"); err != nil {
		t.Fatal(err)
	}
	nextEvent(t, alice, func(event irc.Event) bool { _, ok := event.(*irc.JoinEvent); return ok })

	// A guest cannot speak as a nickname a live client holds, even ephemerally.
	var inUse *irc.NickInUseError
	if _, err := irc.Dial(irc.Config{Nick: "alice", Addr: address, Ephemeral: true}); !errors.As(err, &inUse) || inUse.Nick != "alice" {
		t.Fatalf("one-shot guest on a live nickname: %v", err)
	}
	// Another one-shot session must not join, or announce a quit.
	receipt := sendOneShot(t, "alice-cli", address, "#room", "from a one-shot session")
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
	event := nextEvent(t, alice, func(event irc.Event) bool {
		_, agent := event.(*irc.AgentsEvent)
		_, end := event.(*irc.EndOfAgentsEvent)
		return agent || end
	})
	if agent, ok := event.(*irc.AgentsEvent); ok {
		t.Fatalf("one-shot session listed as agent: %#v", agent)
	}
	if _, err := irc.Dial(irc.Config{Nick: "alice", Addr: address}); err == nil {
		t.Fatal("alice's nickname was released by a one-shot session")
	}
}

func TestOfflineDirectMessagesAreQueuedAndReadable(t *testing.T) {
	address := startServer(t, server.Config{HistoryLimit: 32}, nil)
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
	address := startServer(t, server.Config{}, nil)
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
	address := startServer(t, server.Config{HistoryLimit: 12}, nil)
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
	for i := range 5 {
		sendOneShot(t, "writer", address, "#log", fmt.Sprintf("later %d", i))
	}
	got, status := readHistory(t, reader, "#log", first.ID, 10)
	if status != "expired" || len(got) == 0 || got[len(got)-1].Message != "later 4" {
		t.Fatalf("expired cursor = %v %q", bodies(got), status)
	}
}
