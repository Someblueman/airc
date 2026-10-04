package backend

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestPollMutationProofTimeoutAndUnexpectedReceiptNeverWrite(t *testing.T) {
	target, id := "#room", strings.Repeat("a", 32)
	result := irc.ChatEntry{Action: "results", Target: target, ID: id, From: "reader", Text: "question", Options: []string{"a", "b"}, Votes: []int{0, 0}, ExpiresAt: time.Now().UTC().Add(time.Hour)}
	metadata := protocol.MessageMetadata{Receipt: &protocol.ReceiptInfo{Accepted: true}, ID: id, From: "reader", Target: target, Message: "question", Timestamp: time.Now().UTC()}
	for _, tc := range []struct {
		name, wire string
		timeout    time.Duration
	}{
		{"deadline", "hold", 30 * time.Millisecond},
		{"unexpected-receipt", ":server 762 reader :" + protocol.EncodeMessageMetadata(metadata) + "\r\n", time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, commands, stop := pollMutationFixture(t, target, result, false, false, tc.wire, "")
			defer stop()
			ctx, cancel := context.WithTimeout(context.Background(), tc.timeout)
			defer cancel()
			r := core.Request{Version: 1, RequestID: "poll-proof-" + tc.name, Operation: core.PollVote, Target: &core.Target{Kind: "room", Name: target}, PollID: id, Choice: 1}
			if err := r.Normalize(); err != nil {
				t.Fatal(err)
			}
			a := store.Attempt{Kind: store.SendAttempt, Deadline: time.Now().Add(time.Second), Job: store.Job{Request: r}}
			got := pollMutationExchange(ctx, client, a)
			if got.Poll != nil || got.Completion.MayHavePosted || got.Completion.Failure.Code != core.UpstreamUnavailable || !got.Completion.Failure.Transient {
				t.Fatalf("unproven poll reached write: %+v", got.Completion)
			}
			select {
			case command := <-commands:
				t.Fatalf("unexpected poll mutation: %q", command)
			case <-time.After(50 * time.Millisecond):
			}
		})
	}
}
