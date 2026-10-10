package irc

import (
	"context"
	"testing"
)

func TestRawSignalOwnsMutableCommandFields(t *testing.T) {
	c := &Client{signalsRaw: true, events: make(chan Event, 1)}
	command, err := c.dispatch(context.Background(), "@trace=one :server 780 reader :{bad-json")
	if err != nil {
		t.Fatal(err)
	}
	event := (<-c.events).(*RawEvent)
	command.Params[0] = "other"
	command.Tags["trace"] = "other"
	if event.Params[0] != "reader" || event.Tags["trace"] != "one" {
		t.Fatal("raw signal aliases parsed command")
	}
	event.Params[0] = "event"
	event.Tags["trace"] = "event"
	if command.Params[0] != "other" || command.Tags["trace"] != "other" {
		t.Fatal("parsed command aliases raw signal")
	}
}
