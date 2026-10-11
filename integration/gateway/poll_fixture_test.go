//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func seedAndClosePoll(t *testing.T, f *reactionTransportFixture, seconds int64) (core.PollResult, string) {
	return seedAndClosePollAt(t, f.socket, seconds)
}

func seedAndClosePollAt(t *testing.T, socket string, seconds int64) (core.PollResult, string) {
	t.Helper()
	author := dial(t, socket, "fixture-author", fixtureAccount, false, true)
	defer author.Close()
	if err := author.Chat(irc.ChatRequest{Action: "poll", Target: "#gateway-fixture", Text: "Which snowman? café ☃", Options: []string{"Yes ☃", "No café"}, Seconds: seconds}); err != nil {
		t.Fatal(err)
	}
	created := next(t, author, func(e *irc.ChatEvent) bool { return e.Action == "results" && len(e.Options) == 2 })
	pollID := created.ID
	if created.Target != "#gateway-fixture" || created.Text != "Which snowman? café ☃" || created.From != "fixture-author" || len(created.Votes) != 2 || created.Votes[0] != 0 || created.Votes[1] != 0 {
		t.Fatalf("native poll seed mismatch: %+v", created)
	}
	next(t, author, func(e *irc.EndOfChatEvent) bool { return true })
	if err := author.Chat(irc.ChatRequest{Action: "vote", ID: pollID, Choice: 1}); err != nil {
		t.Fatal(err)
	}
	voted := next(t, author, func(e *irc.ChatEvent) bool { return e.Action == "results" && e.ID == pollID })
	next(t, author, func(e *irc.EndOfChatEvent) bool { return true })
	if len(voted.Votes) != 2 || voted.Votes[0] != 1 || voted.Votes[1] != 0 {
		t.Fatalf("native poll vote mismatch: %+v", voted)
	}
	if err := author.Chat(irc.ChatRequest{Action: "close-poll", ID: pollID}); err != nil {
		t.Fatal(err)
	}
	closed := next(t, author, func(e *irc.ChatEvent) bool { return e.Action == "results" && e.ID == pollID && e.Closed })
	next(t, author, func(e *irc.EndOfChatEvent) bool { return true })
	want := core.PollResult{Target: core.Target{Kind: "room", Name: closed.Target}, ID: closed.ID, From: closed.From, AccountID: closed.AccountID, Question: closed.Text, Options: closed.Options, Votes: closed.Votes, ExpiresAt: closed.ExpiresAt, Closed: closed.Closed}
	return want, pollID
}

func seedPollOnlyAt(t *testing.T, socket string, seconds int64) (core.PollResult, string) {
	t.Helper()
	author := dial(t, socket, "fixture-author", fixtureAccount, false, true)
	defer author.Close()
	if err := author.Chat(irc.ChatRequest{Action: "poll", Target: "#gateway-fixture", Text: "short lived ☃", Options: []string{"one", "two"}, Seconds: seconds}); err != nil {
		t.Fatal(err)
	}
	created := next(t, author, func(e *irc.ChatEvent) bool { return e.Action == "results" })
	next(t, author, func(e *irc.EndOfChatEvent) bool { return true })
	return core.PollResult{Target: core.Target{Kind: "room", Name: created.Target}, ID: created.ID, From: created.From, AccountID: created.AccountID, Question: created.Text, Options: created.Options, Votes: created.Votes, ExpiresAt: created.ExpiresAt, Closed: created.Closed}, created.ID
}

func applyPollVoteAndClose(t *testing.T, socket, pollID string) core.PollResult {
	t.Helper()
	author := dial(t, socket, "fixture-author", fixtureAccount, false, true)
	defer author.Close()
	if err := author.Chat(irc.ChatRequest{Action: "vote", ID: pollID, Choice: 1}); err != nil {
		t.Fatal(err)
	}
	next(t, author, func(e *irc.ChatEvent) bool { return e.Action == "results" && e.ID == pollID && e.Votes[0] == 1 })
	next(t, author, func(e *irc.EndOfChatEvent) bool { return true })
	if err := author.Chat(irc.ChatRequest{Action: "close-poll", ID: pollID}); err != nil {
		t.Fatal(err)
	}
	closed := next(t, author, func(e *irc.ChatEvent) bool { return e.Action == "results" && e.ID == pollID && e.Closed })
	next(t, author, func(e *irc.EndOfChatEvent) bool { return true })
	return core.PollResult{Target: core.Target{Kind: "room", Name: closed.Target}, ID: closed.ID, From: closed.From, AccountID: closed.AccountID, Question: closed.Text, Options: closed.Options, Votes: closed.Votes, ExpiresAt: closed.ExpiresAt, Closed: closed.Closed}
}

func seedForeignPoll(t *testing.T, f *reactionTransportFixture) (core.PollResult, string) {
	t.Helper()
	author := dial(t, f.socket, "fixture-author", fixtureAccount, false, true)
	defer author.Close()
	if err := author.Chat(irc.ChatRequest{Action: "poll", Target: "#other-private", Text: "foreign poll", Options: []string{"yes", "no"}, Seconds: 60}); err != nil {
		t.Fatal(err)
	}
	created := next(t, author, func(e *irc.ChatEvent) bool { return e.Action == "results" })
	next(t, author, func(e *irc.EndOfChatEvent) bool { return true })
	return core.PollResult{Target: core.Target{Kind: "room", Name: created.Target}, ID: created.ID, From: created.From, AccountID: created.AccountID, Question: created.Text, Options: created.Options, Votes: created.Votes, ExpiresAt: created.ExpiresAt, Closed: created.Closed}, created.ID
}

func pollErrorCode(body []byte, want core.ErrorCode) bool {
	var result struct {
		Version int              `json:"version"`
		Error   core.Failure     `json:"error"`
		Poll    *core.PollResult `json:"poll"`
	}
	return json.Unmarshal(body, &result) == nil && result.Version == 1 && result.Error.Code == want && result.Poll == nil
}

func pollHeaderGET(t *testing.T, f *reactionTransportFixture, path string) (int, []byte) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, f.httpURL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	setAuthorHeaders(request, f.authorSpec)
	response, err := f.http.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	wantProtocol := 1
	if f.http.Transport.(*http.Transport).ForceAttemptHTTP2 {
		wantProtocol = 2
	}
	if response.ProtoMajor != wantProtocol {
		t.Fatalf("poll HTTP protocol=%s want major%d", response.Proto, wantProtocol)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, body
}

func evictPollHistory(t *testing.T, socket, pollID string) {
	t.Helper()
	author := dial(t, socket, "fixture-author", fixtureAccount, false, true)
	defer author.Close()
	for i := 0; i < 40; i++ {
		id := fmt.Sprintf("poll-evict-%03d", i)
		if err := author.SendWithID("#gateway-fixture", "evict "+id, id); err != nil {
			t.Fatal(err)
		}
		next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == id })
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	entries, err := irc.RequestCheck(ctx, author, irc.CheckRequest{Targets: []irc.CheckTarget{{Target: "#gateway-fixture", Limit: 64}}, IncludeOwn: true, MaxMessages: 64}, nil)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	latest := false
	for _, entry := range entries {
		if entry.Message != nil {
			count++
			if entry.Message.ID == pollID {
				t.Fatal("poll question not evicted")
			}
			if entry.Message.Message == "evict poll-evict-039" {
				latest = true
			}
		}
	}
	if count != 32 || !latest {
		t.Fatalf("native eviction control count=%d latest=%v", count, latest)
	}
}
