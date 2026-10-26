package backend

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestReplySignalRejectsMalformedParentBeforeWrite(t *testing.T) {
	tests := []struct {
		name, from, parentTarget, requestKind, requestTarget string
	}{
		{name: "room from", from: " author ", parentTarget: "#room", requestKind: "room", requestTarget: "#room"},
		{name: "direct incoming from", from: " Peer ", parentTarget: "Reader", requestKind: "direct", requestTarget: "peer"},
		{name: "direct outgoing target", from: "Reader", parentTarget: " Peer ", requestKind: "direct", requestTarget: "peer"},
	}
	for _, tc := range tests {
		for _, operation := range []core.Operation{core.ReplySignalSet, core.ReplySignalCancel} {
			for _, shape := range []string{"canonical", "malformed"} {
				t.Run(tc.name+"/"+string(operation)+"/"+shape, func(t *testing.T) {
					parent := strings.Repeat("e", 32)
					opName := strings.ReplaceAll(string(operation), ".", "-")
					request := core.Request{
						Version:   1,
						RequestID: "signal-boundary-" + strings.ReplaceAll(tc.name, " ", "-") + "-" + opName,
						Operation: operation,
						Target:    &core.Target{Kind: tc.requestKind, Name: tc.requestTarget},
						ParentID:  parent,
						ReplySignal: func() *core.ReplySignalInput {
							if operation == core.ReplySignalSet {
								return &core.ReplySignalInput{Note: "signal", TTLSeconds: 10}
							}
							return nil
						}(),
					}
					if err := request.Normalize(); err != nil {
						t.Fatalf("invalid test intent: %v", err)
					}
					message := irc.MessageMetadata{
						ID:        parent,
						Kind:      "",
						Target:    tc.parentTarget,
						From:      tc.from,
						Message:   "question",
						Timestamp: time.Now().UTC(),
					}
					if shape == "canonical" {
						message.From = strings.TrimSpace(message.From)
						message.Target = strings.TrimSpace(message.Target)
					}
					proof := replySignalLine(t, irc.ChatEntry{Action: "context-message", Message: &message}) +
						replySignalLine(t, irc.ChatEntry{Action: "context", Context: &irc.ContextSummary{TriggerID: parent, RootID: parent}})
					ack := irc.ChatEntry{Action: "cancel", ID: parent, Target: strings.TrimSpace(tc.parentTarget), From: "reader", ExpiresAt: time.Now().UTC()}
					if operation == core.ReplySignalSet {
						ack.Action = "prepare"
						ack.AccountID = strings.Repeat("a", 32)
						ack.Text = "signal"
						ack.ExpiresAt = time.Now().Add(time.Second).UTC()
					}
					client, commands, stop := replySignalFixture(t, proof, replySignalLine(t, ack), true)
					defer stop()

					attempt := store.Attempt{
						Kind:     store.SendAttempt,
						Deadline: time.Now().Add(2 * time.Second),
						Job:      store.Job{Request: request},
					}
					got := replySignalExchange(context.Background(), client, attempt)
					if shape == "canonical" {
						if got.ReplySignal == nil || got.Completion.Failure.Code != "" {
							t.Fatalf("canonical control failed: %+v", got)
						}
						select {
						case command := <-commands:
							if !strings.Contains(command, `"id":"`+parent+`"`) {
								t.Fatalf("wrong canonical command: %q", command)
							}
						case <-time.After(time.Second):
							t.Fatal("canonical control did not write")
						}
						return
					}
					if got.Completion.Failure.Code != core.UpstreamUnavailable || got.Completion.MayHavePosted || got.ReplySignal != nil {
						t.Fatalf("malformed parent crossed write boundary: completion=%+v acknowledgement=%+v", got.Completion, got.ReplySignal)
					}
					requireNoReplySignalWrite(t, client, commands)
				})
			}
		}
	}
}
