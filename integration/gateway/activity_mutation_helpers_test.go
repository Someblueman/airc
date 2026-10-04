//go:build darwin || linux

package gateway_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func newActivityObserver(t *testing.T, socket string, target core.Target) *irc.Client {
	t.Helper()
	nick := target.Name
	if target.Kind == "room" {
		nick = fmt.Sprintf("activity-reader-%d", roomReaderSequence.Add(1))
	}
	observer := dial(t, socket, nick, "", false, false)
	t.Cleanup(func() { _ = observer.Close() })
	if target.Kind == "room" {
		if err := observer.Join(target.Name); err != nil {
			t.Fatal(err)
		}
		next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == nick && e.Channel == target.Name })
	}
	return observer
}

func nextActivitySignal(t *testing.T, observer *irc.Client) *irc.SignalEvent {
	t.Helper()
	return next(t, observer, func(e *irc.SignalEvent) bool { return e.Action == "typing" || e.Action == "thinking" })
}

func assertActivityMutationFacts(t *testing.T, result core.Result, request core.Request, signal *irc.SignalEvent) {
	t.Helper()
	if result.State != core.Accepted || !result.Durable || result.Accepted == nil || !*result.Accepted || result.Activity == nil || result.SendAttempts != 1 || result.LookupAttempts != 0 || result.MessageID != "" || result.Persisted != nil || result.RecipientConnected != nil || result.Card != nil || result.Topic != nil || result.Room != nil || result.Pin != nil {
		t.Fatalf("activity mutation facts: %+v", result)
	}
	if _, err := core.ActivityAckBytes(result.Activity, request, "fixture-author"); err != nil {
		t.Fatal(err)
	}
	if signal != nil {
		ack := result.Activity
		if ack.Kind != signal.Action || ack.Target.Name != signal.Target || ack.From != signal.From || ack.AccountID != signal.AccountID || ack.Note != signal.Text || !ack.ExpiresAt.Equal(signal.ExpiresAt) || signal.ID != "" {
			t.Fatalf("native observer differs from saved ACK: ack=%+v signal=%+v", ack, signal)
		}
	}
}

func setActivityNative(t *testing.T, socket string, target core.Target, input core.ActivityInput) irc.ChatEntry {
	t.Helper()
	author := dial(t, socket, "fixture-author", fixtureAccount, false, true)
	defer author.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entries, err := irc.RequestChat(ctx, author, irc.ChatRequest{Action: input.Kind, Target: target.Name, Seconds: input.TTLSeconds, Text: input.Note}, nil)
	if err != nil || len(entries) != 1 {
		t.Fatalf("native activity change: entries=%+v error=%v", entries, err)
	}
	return entries[0]
}

func assertNoActivitySignal(t *testing.T, observer *irc.Client) {
	t.Helper()
	timer := time.NewTimer(150 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-observer.Events():
			if !ok {
				t.Fatal("native activity observer disconnected")
			}
			if signal, ok := event.(*irc.SignalEvent); ok {
				t.Fatalf("unexpected native activity broadcast: %+v", signal)
			}
		case <-timer.C:
			return
		}
	}
}
