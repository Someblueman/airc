//go:build darwin || linux

package gateway_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func newReplySignalObserver(t *testing.T, socket string, target core.Target) *irc.Client {
	t.Helper()
	nick := target.Name
	if target.Kind == "room" {
		nick = fmt.Sprintf("reply-signal-reader-%d", roomReaderSequence.Add(1))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Replay checks span multiple subprocess restarts. Keep the independent
	// observer connected across idle periods; each observation remains bounded.
	observer, err := irc.DialContext(ctx, irc.Config{Network: "unix", Addr: socket, Nick: nick, ReadTimeout: 120 * time.Second, WriteTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = observer.Close() })
	if target.Kind == "room" {
		if err := observer.Join(target.Name); err != nil {
			t.Fatal(err)
		}
		next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == nick && e.Channel == target.Name })
	}
	return observer
}

func nextReplySignal(t *testing.T, observer *irc.Client, parentID string) *irc.SignalEvent {
	t.Helper()
	return next(t, observer, func(e *irc.SignalEvent) bool {
		return e.ID == parentID && (e.Action == "prepare" || e.Action == "cancel")
	})
}

func assertReplySignalFacts(t *testing.T, result core.Result, request core.Request, signal *irc.SignalEvent) {
	t.Helper()
	if result.State != core.Accepted || !result.Durable || result.Accepted == nil || !*result.Accepted || result.ReplySignal == nil || result.SendAttempts != 1 || result.LookupAttempts != 0 || result.MessageID != "" || result.Persisted != nil || result.RecipientConnected != nil || result.Card != nil || result.Topic != nil || result.Room != nil || result.Pin != nil || result.Activity != nil {
		t.Fatalf("reply signal facts: %+v", result)
	}
	if _, err := core.ReplySignalAckBytes(result.ReplySignal, request, "fixture-author"); err != nil {
		t.Fatal(err)
	}
	if signal != nil {
		ack := result.ReplySignal
		if ack.Action != signal.Action || ack.NativeTarget != signal.Target || ack.ParentID != signal.ID || ack.From != signal.From || ack.AccountID != signal.AccountID || ack.Note != signal.Text || !ack.ExpiresAt.Equal(signal.ExpiresAt) {
			t.Fatalf("native reply signal differs from saved ACK: ack=%+v signal=%+v", ack, signal)
		}
	}
}

func setReplySignalNative(t *testing.T, socket, parentID string, input *core.ReplySignalInput) irc.ChatEntry {
	t.Helper()
	author := dial(t, socket, "fixture-author", fixtureAccount, false, true)
	defer author.Close()
	r := irc.ChatRequest{Action: "cancel", ID: parentID}
	if input != nil {
		r.Action = "prepare"
		r.Seconds = input.TTLSeconds
		r.Text = input.Note
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entries, err := irc.RequestChat(ctx, author, r, nil)
	if err != nil || len(entries) != 1 {
		t.Fatalf("native reply signal write: entries=%+v error=%v", entries, err)
	}
	return entries[0]
}

func assertNoReplySignal(t *testing.T, observer *irc.Client) {
	t.Helper()
	assertNoActivitySignal(t, observer)
}

func queryNativeWaitingSignal(t *testing.T, socket, parentID string) []irc.ChatEntry {
	t.Helper()
	reader := dial(t, socket, "fixture-author", fixtureAccount, false, true)
	defer reader.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entries, err := irc.RequestChat(ctx, reader, irc.ChatRequest{Action: "waiting", ID: parentID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func replySignalRequest(id, parent string, operation core.Operation, target *core.Target) core.Request {
	r := core.Request{Version: 1, RequestID: id, Operation: operation, Target: target, ParentID: parent}
	if operation == core.ReplySignalSet {
		r.ReplySignal = &core.ReplySignalInput{Note: "coming soon", TTLSeconds: 120}
	}
	return r
}
