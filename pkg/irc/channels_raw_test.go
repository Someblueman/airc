package irc_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

func TestChannelsRawPreservesNumericEnvelopeAndRestoresTypedEvents(t *testing.T) {
	client := dialTestClient(t, "channels-raw")
	if err := client.Join("#raw-room"); err != nil {
		t.Fatal(err)
	}
	nextIRCEvent(t, client, func(event irc.Event) bool {
		value, ok := event.(*irc.JoinEvent)
		return ok && value.Channel == "#raw-room"
	})
	if err := client.ChannelsRaw(); err != nil {
		t.Fatal(err)
	}
	rawEvent := nextIRCEvent(t, client, func(event irc.Event) bool {
		value, ok := event.(*irc.RawEvent)
		return ok && value.Command == "768"
	})
	raw := rawEvent.(*irc.RawEvent)
	if len(raw.Params) != 1 || raw.Params[0] != client.Nick() || raw.Trailing == "" {
		t.Fatalf("raw 768=%+v", raw)
	}
	var info irc.ChannelInfo
	if err := json.Unmarshal([]byte(raw.Trailing), &info); err != nil || info.Name != "#raw-room" || info.Members != 1 {
		t.Fatalf("raw 768 payload=%q info=%+v err=%v", raw.Trailing, info, err)
	}
	endRaw := nextIRCEvent(t, client, func(event irc.Event) bool {
		value, ok := event.(*irc.RawEvent)
		return ok && value.Command == "769"
	})
	end := endRaw.(*irc.RawEvent)
	if len(end.Params) != 1 || end.Params[0] != client.Nick() || end.Trailing != "End of channels list" {
		t.Fatalf("raw 769=%+v", end)
	}
	if err := client.Channels(); err != nil {
		t.Fatal(err)
	}
	typedChannel := nextIRCEvent(t, client, func(event irc.Event) bool {
		_, ok := event.(*irc.ChannelEvent)
		return ok
	})
	if typedChannel.(*irc.ChannelEvent).Channel != info {
		t.Fatalf("typed channel=%+v want=%+v", typedChannel, info)
	}
	typed := nextIRCEvent(t, client, func(event irc.Event) bool {
		_, ok := event.(*irc.EndOfChannelsEvent)
		return ok
	})
	if typed == nil {
		t.Fatal("typed CHANNELS terminator missing")
	}
}

func dialTestClient(t *testing.T, nick string) *irc.Client {
	t.Helper()
	_, listener := startServer(t, "127.0.0.1:0")
	client, err := irc.DialContext(context.Background(), irc.Config{Nick: nick, Addr: listener.Addr().String(), ReadTimeout: time.Second, WriteTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func nextIRCEvent(t *testing.T, client *irc.Client, match func(irc.Event) bool) irc.Event {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				t.Fatal("client event stream closed")
			}
			if match(event) {
				return event
			}
		case <-deadline.C:
			t.Fatal("timed out waiting for IRC event")
		}
	}
}
