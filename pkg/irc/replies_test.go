package irc_test

import (
	"fmt"
	"testing"

	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestReplySubscriptionsDeliverOnceWithLiveMetadata(t *testing.T) {
	address := startConfigured(t, server.Config{HistoryLimit: 32}, "")
	root := sendOneShot(t, "alice", address, "#room", "question")
	reader := dialOneShot(t, "human", address)
	if err := reader.Observe("#room", "thread:"+root.ID, "replies:"+root.ID, "@human"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		nextEvent(t, reader, func(e irc.Event) bool { raw, ok := e.(*irc.RawEvent); return ok && raw.Command == "765" })
	}
	writer := dialOneShot(t, "bob", address)
	if err := writer.Reply(root.ID, "@human answer\nsecond line"); err != nil {
		t.Fatal(err)
	}
	receipt := nextEvent(t, writer, func(e irc.Event) bool { _, ok := e.(*irc.SendReceiptEvent); return ok }).(*irc.SendReceiptEvent)
	if err := writer.Send("#room", "marker"); err != nil {
		t.Fatal(err)
	}
	var got []string
	for {
		message := nextEvent(t, reader, func(e irc.Event) bool { _, ok := e.(*irc.MessageEvent); return ok }).(*irc.MessageEvent)
		got = append(got, message.Message)
		if message.Message == "marker" {
			break
		}
		if message.ID != receipt.ID || message.ReplyTo != root.ID || message.ThreadID != root.ID {
			t.Fatalf("lost live metadata: %+v", message)
		}
	}
	if fmt.Sprint(got) != fmt.Sprint([]string{"@human answer\nsecond line", "marker"}) {
		t.Fatalf("overlapping subscriptions: %v", got)
	}
	history, _ := readHistory(t, reader, "thread:"+root.ID, "*", 10)
	if len(history) != 2 || history[1].ReplyTo != root.ID || history[1].ThreadID != root.ID || receipt.MessageEvent().ReplyTo != root.ID {
		t.Fatalf("history/receipt metadata: %+v %+v", history, receipt)
	}
}
