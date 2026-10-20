package server

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

// listing sends line on c and collects the events of type T until one of
// type End.
func listing[T, End irc.Event](t *testing.T, c *irc.Client, line string) []T {
	t.Helper()
	if err := c.Raw(line); err != nil {
		t.Fatal(err)
	}
	var out []T
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e := <-c.Events():
			switch v := e.(type) {
			case T:
				out = append(out, v)
			case End:
				return out
			}
		case <-timeout:
			t.Fatalf("no end of %q", line)
		}
	}
}

func oneShot(t *testing.T, address, nick string) *irc.Client {
	t.Helper()
	c, err := irc.Dial(irc.Config{Nick: nick, Addr: address, Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestOneShotAgentsAreActiveForTenMinutes(t *testing.T) {
	var mu sync.Mutex
	now := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	advance := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }
	address, _ := startServer(t, Config{HistoryLimit: 64, PingInterval: time.Hour, Now: clock}, nil)
	ctx := context.Background()

	// A one-shot agent checks #work and posts in #ops, then disconnects.
	builder := oneShot(t, address, "builder")
	if _, err := irc.RequestCheck(ctx, builder, irc.CheckRequest{Targets: []irc.CheckTarget{{Target: "#work", Limit: 10}}, MaxMessages: 10}, nil); err != nil {
		t.Fatal(err)
	}
	if err := builder.Send("#ops", "deploying"); err != nil {
		t.Fatal(err)
	}
	for e := range builder.Events() {
		if _, ok := e.(*irc.SendReceiptEvent); ok {
			break
		}
	}
	builder.Close()
	// Anonymous reads are not activity.
	reader := oneShot(t, address, "observer-1a2b")
	listing[*irc.HistoryEvent, *irc.EndOfHistoryEvent](t, reader, "HISTORY #work")

	planner := oneShot(t, address, "planner")
	card := func(nick string) irc.AgentCard {
		t.Helper()
		cards := listing[*irc.DirectoryEvent, *irc.EndOfDirectoryEvent](t, planner, "DIRECTORY "+nick)
		if len(cards) != 1 {
			t.Fatalf("DIRECTORY %s: %+v", nick, cards)
		}
		return cards[0].AgentCard
	}
	if c := card("builder"); !c.Active || c.Connected || !slices.Equal(c.Rooms, []string{"#ops", "#work"}) || !c.LastSeen.Equal(clock()) {
		t.Fatalf("builder's card %+v", c)
	}
	var listed []string
	for _, c := range listing[*irc.DirectoryEvent, *irc.EndOfDirectoryEvent](t, planner, "DIRECTORY") {
		listed = append(listed, c.Nick)
	}
	if !slices.Contains(listed, "builder") || slices.Contains(listed, "observer-1a2b") {
		t.Fatalf("the directory lists %v", listed)
	}

	// AGENTS keeps its meaning for older clients; ACTIVE adds one-shot agents.
	if plain := listing[*irc.AgentsEvent, *irc.EndOfAgentsEvent](t, planner, "AGENTS"); len(plain) != 0 {
		t.Fatalf("plain AGENTS lists %+v", plain)
	}
	active := listing[*irc.AgentsEvent, *irc.EndOfAgentsEvent](t, planner, "AGENTS ACTIVE")
	if len(active) != 1 || active[0].Agent.Nick != "builder" || !slices.Equal(active[0].Agent.Channels, []string{"#ops", "#work"}) || !active[0].Agent.ActiveAt.Equal(clock()) || !active[0].Agent.ConnectedAt.IsZero() {
		t.Fatalf("AGENTS ACTIVE lists %+v", active)
	}

	// A direct message to an agent at work says so, though it holds no connection.
	if err := planner.Send("builder", "can you review?"); err != nil {
		t.Fatal(err)
	}
	var receipt *irc.SendReceiptEvent
	for e := range planner.Events() {
		if r, ok := e.(*irc.SendReceiptEvent); ok {
			receipt = r
			break
		}
	}
	if receipt == nil || receipt.Receipt == nil || *receipt.Receipt.RecipientConnected || !*receipt.Receipt.RecipientActive {
		t.Fatalf("receipt %+v", receipt)
	}

	// Ten minutes later it is no longer active, though it was last seen.
	advance(activeWindow)
	if c := card("builder"); c.Active || len(c.Rooms) != 0 || c.LastSeen.IsZero() {
		t.Fatalf("builder's card after the window %+v", c)
	}
	if active := listing[*irc.AgentsEvent, *irc.EndOfAgentsEvent](t, planner, "AGENTS ACTIVE"); len(active) != 0 {
		t.Fatalf("AGENTS ACTIVE after the window lists %+v", active)
	}
}

func TestActivityStaysBounded(t *testing.T) {
	s := New(Config{HistoryLimit: 8})
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range maxActivity + 10 {
		s.noteActivityLocked(fmt.Sprintf("agent%d", i), "#r")
	}
	for i := range maxActivityRooms + 5 {
		s.noteActivityLocked("busy", "#room"+string(rune('a'+i)))
	}
	if len(s.activity) > maxActivity || len(s.activity["busy"].rooms) != maxActivityRooms {
		t.Fatalf("%d agents, %d rooms", len(s.activity), len(s.activity["busy"].rooms))
	}
}
