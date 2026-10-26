//go:build darwin || linux

package gateway_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func pollMutationRequest(id string, op core.Operation, target *core.Target, pollID string) core.Request {
	r := core.Request{Version: 1, RequestID: id, Operation: op, Target: target, PollID: pollID}
	if op == core.PollCreate {
		r.Poll = &core.PollInput{Question: "Which route?", Options: []string{"north", "south"}, TTLSeconds: 120}
	}
	if op == core.PollVote {
		r.Choice = 2
	}
	return r
}

func nativePollEntry(t *testing.T, socket, id string) irc.ChatEntry {
	t.Helper()
	reader := dial(t, socket, "fixture-author", fixtureAccount, false, true)
	defer reader.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entries, err := irc.RequestChat(ctx, reader, irc.ChatRequest{Action: "results", ID: id}, nil)
	if err != nil || len(entries) != 1 || entries[0].Action != "results" || entries[0].ID != id {
		t.Fatalf("native poll query: entries=%+v error=%v", entries, err)
	}
	return entries[0]
}

func assertPollMutationFacts(t *testing.T, result core.Result, request core.Request, native *irc.ChatEntry) {
	t.Helper()
	if result.State != core.Accepted || !result.Durable || result.Accepted == nil || !*result.Accepted || result.Poll == nil || result.SendAttempts != 1 || result.LookupAttempts != 0 || result.Card != nil || result.Topic != nil || result.Room != nil || result.Pin != nil || result.Activity != nil || result.ReplySignal != nil {
		t.Fatalf("poll mutation facts: %+v", result)
	}
	if _, err := core.PollAckBytes(result.Poll, request, "fixture-author"); err != nil {
		t.Fatal(err)
	}
	ack := result.Poll
	if request.Operation == core.PollCreate {
		if ack.Receipt == nil || result.MessageID != ack.Poll.ID || result.Persisted == nil || !reflect.DeepEqual(result.RecipientConnected, ack.Receipt.Receipt.RecipientConnected) || *result.Persisted != ack.Receipt.Receipt.Persisted {
			t.Fatalf("poll creation lost native receipt facts: %+v", result)
		}
	} else if ack.Receipt != nil || result.MessageID != "" || result.Persisted != nil || result.RecipientConnected != nil {
		t.Fatalf("poll vote/close fabricated receipt facts: %+v", result)
	}
	if native != nil {
		want := core.PollResult{Target: *request.Target, ID: native.ID, From: native.From, AccountID: native.AccountID, Question: native.Text, Options: native.Options, Votes: native.Votes, ExpiresAt: native.ExpiresAt, Closed: native.Closed}
		if ack.Action != native.Action || !reflect.DeepEqual(ack.Poll, want) {
			t.Fatalf("native poll differs from historical ACK: ack=%+v native=%+v", ack, native)
		}
	}
}

func nextPollMutationSignal(t *testing.T, observer *irc.Client, id string) *irc.SignalEvent {
	t.Helper()
	return next(t, observer, func(e *irc.SignalEvent) bool { return e.Action == "results" && e.ID == id })
}

func assertNoPollMutation(t *testing.T, observer *irc.Client, pollID string) {
	t.Helper()
	timer := time.NewTimer(150 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-observer.Events():
			if !ok {
				t.Fatal("poll observer disconnected")
			}
			switch value := event.(type) {
			case *irc.MessageEvent:
				if value.Kind == "poll" {
					t.Fatalf("poll replay created native poll: %+v", value)
				}
			case *irc.SignalEvent:
				if value.ID == pollID && value.Action == "results" {
					t.Fatalf("poll replay changed native result: %+v", value)
				}
			}
		case <-timer.C:
			return
		}
	}
}
