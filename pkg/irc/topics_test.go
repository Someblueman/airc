package irc_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/pkg/irc"
)

func queryTopic(t *testing.T, c *irc.Client, channel string) *irc.TopicEvent {
	t.Helper()
	if err := c.Topic(channel); err != nil {
		t.Fatal(err)
	}
	return nextEvent(t, c, func(event irc.Event) bool {
		e, ok := event.(*irc.TopicEvent)
		return ok && e.Channel == channel
	}).(*irc.TopicEvent)
}

func TestChannelTopicIsSetQueriedBroadcastAndCleared(t *testing.T) {
	address := startServer(t, server.Config{HistoryLimit: 8}, nil)
	setter := dialOneShot(t, "dana", address)
	watcher := dialOneShot(t, "erin", address)
	if err := watcher.Observe("#hq"); err != nil {
		t.Fatal(err)
	}
	nextEvent(t, watcher, func(event irc.Event) bool { r, ok := event.(*irc.RawEvent); return ok && r.Command == "765" })

	if got := queryTopic(t, setter, "#hq"); got.Topic != "" {
		t.Fatalf("a channel starts with no topic, got %q", got.Topic)
	}
	// Nobody is in #hq; the one-shot setter still sets it.
	if err := setter.SetTopic("#hq", "Welcome! Post status here, tag @planner for decisions."); err != nil {
		t.Fatal(err)
	}
	live := nextEvent(t, watcher, func(event irc.Event) bool { _, ok := event.(*irc.TopicEvent); return ok }).(*irc.TopicEvent)
	if live.Topic != "Welcome! Post status here, tag @planner for decisions." || live.SetBy != "dana" || live.Channel != "#hq" {
		t.Fatalf("observer saw %#v", live)
	}
	if got := queryTopic(t, watcher, "#hq"); got.Topic != live.Topic {
		t.Fatalf("query = %q", got.Topic)
	}

	// A member who joins is shown the header, as on any IRC network.
	member := newClient(t, "frank", address)
	if err := member.Join("#hq"); err != nil {
		t.Fatal(err)
	}
	if got := nextEvent(t, member, func(event irc.Event) bool { _, ok := event.(*irc.TopicEvent); return ok }).(*irc.TopicEvent); got.Topic != live.Topic {
		t.Fatalf("join showed %q", got.Topic)
	}

	// Invalid headers are refused and change nothing.
	for name, bad := range map[string]string{"too long": strings.Repeat("x", 401), "control char": "tab\there"} {
		if err := setter.SetTopic("#hq", bad); err != nil {
			t.Fatal(err)
		}
		nextEvent(t, setter, func(event irc.Event) bool { r, ok := event.(*irc.RawEvent); return ok && r.Command == "417" })
		if got := queryTopic(t, setter, "#hq"); got.Topic != live.Topic {
			t.Fatalf("%s: topic changed to %q", name, got.Topic)
		}
	}

	if err := setter.SetTopic("#hq", ""); err != nil {
		t.Fatal(err)
	}
	if got := nextEvent(t, watcher, func(event irc.Event) bool { _, ok := event.(*irc.TopicEvent); return ok }).(*irc.TopicEvent); got.Topic != "" {
		t.Fatalf("clearing was reported as %q", got.Topic)
	}
	if got := queryTopic(t, setter, "#hq"); got.Topic != "" {
		t.Fatalf("topic not cleared: %q", got.Topic)
	}
}

func TestChannelTopicsSurviveServerRestart(t *testing.T) {
	file := filepath.Join(t.TempDir(), "topics.json")
	run := func(set string) string {
		var srv *server.Server
		address := startServer(t, server.Config{}, func(s *server.Server) error {
			srv = s
			return s.RestoreTopics(file)
		})
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = srv.Shutdown(ctx)
		}()
		client := dialOneShot(t, "dana", address)
		if set != "" {
			if err := client.SetTopic("#hq", set); err != nil {
				t.Fatal(err)
			}
			nextEvent(t, client, func(event irc.Event) bool { _, ok := event.(*irc.TopicEvent); return ok })
		}
		return queryTopic(t, client, "#hq").Topic
	}
	if got := run("survives restarts"); got != "survives restarts" {
		t.Fatalf("first run = %q", got)
	}
	if got := run(""); got != "survives restarts" {
		t.Fatalf("after restart = %q", got)
	}
	if info, err := os.Stat(file); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("topics file: %v %v", info, err)
	}
}

func TestChannelDirectoryIncludesChannelsNobodyIsIn(t *testing.T) {
	address := startServer(t, server.Config{HistoryLimit: 16}, nil)
	sendOneShot(t, "a", address, "#alpha", "one")
	sendOneShot(t, "a", address, "#alpha", "two")
	sendOneShot(t, "a", address, "#beta", "three")
	sendOneShot(t, "a", address, "direct-to-someone", "not a channel")
	dialOneShot(t, "setter", address).SetTopic("#gamma", "only a header")
	member := newClient(t, "m", address)
	joinAndWait(t, member, "#delta")

	reader := dialOneShot(t, "reader", address)
	if err := reader.Channels(); err != nil {
		t.Fatal(err)
	}
	got := map[string]irc.ChannelInfo{}
	var order []string
	for {
		event := nextEvent(t, reader, func(event irc.Event) bool {
			switch event.(type) {
			case *irc.ChannelEvent, *irc.EndOfChannelsEvent:
				return true
			}
			return false
		})
		if e, ok := event.(*irc.ChannelEvent); ok {
			got[e.Channel.Name] = e.Channel
			order = append(order, e.Channel.Name)
			continue
		}
		break
	}
	if fmt.Sprint(order) != "[#alpha #beta #delta #gamma]" {
		t.Fatalf("channels = %v, want sorted and without direct-message targets", order)
	}
	if got["#alpha"].Messages != 2 || got["#alpha"].LastActivity.IsZero() || got["#beta"].Messages != 1 {
		t.Fatalf("message counts wrong: %#v", got)
	}
	if got["#gamma"].Topic != "only a header" || got["#delta"].Members != 1 || got["#alpha"].Members != 0 {
		t.Fatalf("topic or members wrong: %#v", got)
	}
}
