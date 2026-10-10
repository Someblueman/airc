package irc_test

import (
	"fmt"
	"testing"

	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestObserveDirectMessagesByNickname(t *testing.T) {
	address := startServer(t, server.Config{HistoryLimit: 8}, nil)
	watcher := dialOneShot(t, "carol", address)
	if err := watcher.Observe("#news", "@Carol"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
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

func TestMentionInboxCollectsTagsAcrossChannelsAndDirectMessages(t *testing.T) {
	address := startServer(t, server.Config{HistoryLimit: 32}, nil)
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
	address := startServer(t, server.Config{HistoryLimit: 32}, nil)
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
