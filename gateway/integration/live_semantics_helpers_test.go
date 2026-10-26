//go:build darwin || linux

package gateway_test

import (
	"bufio"
	"reflect"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/pkg/irc"
)

func liveSemanticEvent(t *testing.T, reader *bufio.Scanner, kind, checkpoint string) *backend.LiveEvent {
	t.Helper()
	page, id := ssePage(t, reader)
	if id != checkpoint || page.Cursor != checkpoint || len(page.Messages) != 0 || len(page.Events) != 1 || page.Status != "ok" || page.Gap {
		t.Fatalf("nonmessage page changed checkpoint or lost records: %+v id=%q want=%q", page, id, checkpoint)
	}
	event := page.Events[0]
	if event.Kind != kind || event.Target != "#gateway-fixture" {
		t.Fatalf("wrong live event: %+v want=%s", event, kind)
	}
	return event
}

func liveSemanticSignal(t *testing.T, native *irc.Client, reader *bufio.Scanner, action, checkpoint string) irc.ChatEntry {
	t.Helper()
	want := nextRawLiveSignal(t, native, action)
	event := liveSemanticEvent(t, reader, "signal", checkpoint)
	if event.Signal == nil || !reflect.DeepEqual(*event.Signal, want) || event.Actor != want.From {
		t.Fatalf("signal lost native fields: gateway=%+v native=%+v", event, want)
	}
	return want
}

func liveSemanticChat(t *testing.T, author *irc.Client, request irc.ChatRequest, resultAction string) *irc.ChatEvent {
	t.Helper()
	if err := author.Chat(request); err != nil {
		t.Fatal(err)
	}
	result := next(t, author, func(event *irc.ChatEvent) bool { return event.Action == resultAction })
	next(t, author, func(event *irc.EndOfChatEvent) bool { return true })
	return result
}

func liveSemanticMessage(t *testing.T, author *irc.Client, parent, id string) *irc.SendReceiptEvent {
	t.Helper()
	var err error
	if parent == "" {
		err = author.SendWithID("#gateway-fixture", id, id)
	} else {
		err = author.ReplyWithID(parent, id, id)
	}
	if err != nil {
		t.Fatal(err)
	}
	return next(t, author, func(event *irc.SendReceiptEvent) bool { return event.RequestID == id })
}
