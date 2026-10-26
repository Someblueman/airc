//go:build darwin || linux

package gateway_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestPollMutationVotePreservesOtherAuthorAndCloseOwnership(t *testing.T) {
	f := newReactionHTTPFixture(t)
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	observer := newReplySignalObserver(t, f.socket, *target)
	author := dial(t, f.socket, "poll-other-author", strings.Repeat("e", 64), true, true)
	if err := author.Chat(irc.ChatRequest{Action: "poll", Target: target.Name, Text: "another author's poll", Options: []string{"one", "two"}, Seconds: 120}); err != nil {
		t.Fatal(err)
	}
	created := next(t, author, func(e *irc.ChatEvent) bool { return e.Action == "results" })
	next(t, author, func(e *irc.EndOfChatEvent) bool { return true })
	if err := author.Close(); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == created.ID && e.Kind == "poll" })
	vote := pollMutationRequest("poll-other-author-vote", core.PollVote, target, created.ID)
	headerPost(t, f, vote)
	result := waitHTTPPinResult(t, f, vote.RequestID, core.Accepted)
	signal := nextPollMutationSignal(t, observer, created.ID)
	assertPollMutationFacts(t, result, vote, &signal.ChatEntry)
	if result.Poll.Poll.From != "poll-other-author" || result.Poll.Poll.AccountID != created.AccountID || result.Poll.Poll.Votes[1] != 1 {
		t.Fatal("vote substituted voter for author", result.Poll)
	}
	before := nativePollEntry(t, f.socket, created.ID)
	closeRequest := pollMutationRequest("poll-other-author-close", core.PollClose, target, created.ID)
	headerPost(t, f, closeRequest)
	denied := waitHTTPPinResult(t, f, closeRequest.RequestID, core.Rejected)
	if denied.Poll != nil || denied.AcceptanceUncertain || denied.Failure == nil || denied.Failure.Code != core.InvalidRequest || denied.Failure.UpstreamCode != 461 {
		t.Fatalf("foreign-author close result: %+v", denied)
	}
	if after := nativePollEntry(t, f.socket, created.ID); !reflect.DeepEqual(after, before) {
		t.Fatal("non-author closed poll", before, after)
	}
	assertNoPollMutation(t, observer, created.ID)
}
