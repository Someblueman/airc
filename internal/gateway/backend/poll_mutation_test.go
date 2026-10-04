package backend

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestPollMutationCreateVoteClose(t *testing.T) {
	target := "#room"
	id := strings.Repeat("a", 32)
	account := strings.Repeat("b", 32)
	expires := time.Now().UTC().Add(time.Hour).Truncate(time.Millisecond)
	result := irc.ChatEntry{Action: "results", Target: target, ID: id, From: "reader", AccountID: account, Text: "Question?", Options: []string{"yes", "no"}, Votes: []int{1, 0}, ExpiresAt: expires}
	for _, tc := range []struct {
		name string
		op   core.Operation
		in   *core.PollInput
		id   string
		vote int
	}{
		{name: "create", op: core.PollCreate, in: &core.PollInput{Question: "Question?", Options: []string{"yes", "no"}, TTLSeconds: 60}},
		{name: "vote", op: core.PollVote, id: id, vote: 1},
		{name: "close", op: core.PollClose, id: id},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caseResult := result
			if tc.op == core.PollCreate {
				caseResult.Votes = []int{0, 0}
			}
			if tc.op == core.PollClose {
				caseResult.Closed = true
			}
			client, commands, stop := pollMutationFixture(t, target, caseResult, tc.op == core.PollCreate, true, "", "")
			defer stop()
			request := core.Request{Version: 1, RequestID: "poll-" + tc.name, Operation: tc.op, Target: &core.Target{Kind: "room", Name: target}, PollID: tc.id, Choice: tc.vote, Poll: tc.in}
			attempt := store.Attempt{Kind: store.SendAttempt, Deadline: time.Now().Add(3 * time.Second), Job: store.Job{Request: request}}
			got := pollMutationExchange(context.Background(), client, attempt)
			if got.Completion.Failure.Code != "" || got.Poll == nil || got.Poll.Poll.ID != id || got.Poll.Poll.Target.Name != target {
				t.Fatalf("outcome=%+v", got)
			}
			select {
			case command := <-commands:
				if !strings.Contains(command, fmt.Sprintf(`"action":"%s"`, map[core.Operation]string{core.PollCreate: "poll", core.PollVote: "vote", core.PollClose: "close-poll"}[tc.op])) {
					t.Fatalf("command=%q", command)
				}
			case <-time.After(time.Second):
				t.Fatal("fixture did not observe poll command")
			}
		})
	}
}

func TestPollMutationForeignPreflightDoesNotWrite(t *testing.T) {
	target := "#room"
	id := strings.Repeat("c", 32)
	result := irc.ChatEntry{Action: "results", Target: "#foreign", ID: id, From: "reader", Text: "Question?", Options: []string{"yes", "no"}, Votes: []int{0, 0}, ExpiresAt: time.Now().UTC().Add(time.Hour)}
	client, commands, stop := pollMutationFixture(t, target, result, false, false, "", "")
	defer stop()
	request := core.Request{Version: 1, RequestID: "poll-foreign", Operation: core.PollVote, Target: &core.Target{Kind: "room", Name: target}, PollID: id, Choice: 1}
	attempt := store.Attempt{Kind: store.SendAttempt, Deadline: time.Now().Add(2 * time.Second), Job: store.Job{Request: request}}
	got := pollMutationExchange(context.Background(), client, attempt)
	if got.Completion.Failure.Code != core.Forbidden || got.Completion.MayHavePosted || got.Poll != nil {
		t.Fatalf("foreign preflight outcome=%+v", got.Completion)
	}
	select {
	case command := <-commands:
		t.Fatalf("foreign preflight reached mutation: %q", command)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestPollMutationMalformedPostWriteIsUncertain(t *testing.T) {
	target := "#room"
	id := strings.Repeat("d", 32)
	result := irc.ChatEntry{Action: "results", Target: target, ID: id, From: "reader", Text: "Question?", Options: []string{"yes", "no"}, Votes: []int{0, 0}, ExpiresAt: time.Now().UTC().Add(time.Hour)}
	bad := result
	bad.Action = "prepare"
	client, commands, stop := pollMutationFixture(t, target, result, false, true, "", "", &bad)
	defer stop()
	request := core.Request{Version: 1, RequestID: "poll-bad", Operation: core.PollVote, Target: &core.Target{Kind: "room", Name: target}, PollID: id, Choice: 1}
	attempt := store.Attempt{Kind: store.SendAttempt, Deadline: time.Now().Add(2 * time.Second), Job: store.Job{Request: request}}
	got := pollMutationExchange(context.Background(), client, attempt)
	if got.Completion.Failure.Code != core.DeliveryUncertain || !got.Completion.MayHavePosted || got.Poll != nil {
		t.Fatalf("malformed post-write outcome=%+v", got.Completion)
	}
	select {
	case command := <-commands:
		if !strings.Contains(command, `"action":"vote"`) {
			t.Fatalf("command=%q", command)
		}
	case <-time.After(time.Second):
		t.Fatal("fixture did not observe vote")
	}
}

func TestPollMutationPreflightNumericsArePermanentAndDoNotWrite(t *testing.T) {
	for _, tc := range []struct {
		code string
		want core.ErrorCode
	}{
		{code: "461", want: core.InvalidRequest},
		{code: "485", want: core.Forbidden},
		{code: "404", want: core.Forbidden},
		{code: "487", want: core.IDConflict},
		{code: "503", want: core.DeliveryUncertain},
	} {
		t.Run(tc.code, func(t *testing.T) {
			target, id := "#room", strings.Repeat("f", 32)
			result := irc.ChatEntry{Action: "results", Target: target, ID: id, From: "reader", Text: "Question?", Options: []string{"yes", "no"}, Votes: []int{0, 0}, ExpiresAt: time.Now().UTC().Add(time.Hour)}
			client, commands, stop := pollMutationFixture(t, target, result, false, false, ":server "+tc.code+" reader :poll failure\r\n", "")
			defer stop()
			request := core.Request{Version: 1, RequestID: "poll-numeric-" + tc.code, Operation: core.PollVote, Target: &core.Target{Kind: "room", Name: target}, PollID: id, Choice: 1}
			attempt := store.Attempt{Kind: store.SendAttempt, Deadline: time.Now().Add(2 * time.Second), Job: store.Job{Request: request}}
			got := pollMutationExchange(context.Background(), client, attempt)
			if got.Completion.Failure.Code != tc.want || got.Completion.Failure.UpstreamCode != mustPollNumeric(t, tc.code) || got.Completion.Failure.Transient != (tc.code == "503") || got.Completion.MayHavePosted || got.Poll != nil {
				t.Fatalf("outcome=%+v", got.Completion)
			}
			select {
			case command := <-commands:
				t.Fatalf("preflight numeric reached mutation: %q", command)
			case <-time.After(50 * time.Millisecond):
			}
		})
	}
}

func mustPollNumeric(t *testing.T, code string) int {
	t.Helper()
	value, err := strconv.Atoi(code)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
