package backend

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestPollMutationPartialAcceptanceThenNumericIsUncertain(t *testing.T) {
	for _, tc := range []struct {
		name   string
		op     core.Operation
		result bool
	}{
		{"create-receipt", core.PollCreate, false},
		{"create-results", core.PollCreate, true},
		{"vote-results", core.PollVote, true},
		{"close-results", core.PollClose, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			op := tc.op
			id := strings.Repeat("a", 32)
			result := irc.ChatEntry{Action: "results", Target: "#room", ID: id, From: "reader", AccountID: strings.Repeat("b", 32), Text: "Question?", Options: []string{"yes", "no"}, Votes: []int{0, 0}, ExpiresAt: time.Now().UTC().Add(time.Hour), Closed: op == core.PollClose}
			client, commands, stop := pollMutationFixture(t, result.Target, result, op == core.PollCreate, tc.result, "", ":server 461 reader :contradictory failure\r\n")
			defer stop()
			request := core.Request{Version: 1, RequestID: "partial-poll", Operation: op, Target: &core.Target{Kind: "room", Name: result.Target}, PollID: id}
			if op == core.PollCreate {
				request.PollID = ""
				request.Poll = &core.PollInput{Question: result.Text, Options: result.Options, TTLSeconds: 60}
			}
			if op == core.PollVote {
				request.Choice = 1
			}
			attempt := store.Attempt{Kind: store.SendAttempt, Deadline: time.Now().Add(2 * time.Second), Job: store.Job{Request: request}}
			got := pollMutationExchange(context.Background(), client, attempt)
			if got.Completion.Failure.Code != core.DeliveryUncertain || !got.Completion.Failure.Transient || got.Completion.Failure.UpstreamCode != 461 || !got.Completion.MayHavePosted || got.Poll != nil {
				t.Fatalf("outcome=%+v", got)
			}
			select {
			case <-commands:
			case <-time.After(time.Second):
				t.Fatal("mutation not observed")
			}
		})
	}
}
