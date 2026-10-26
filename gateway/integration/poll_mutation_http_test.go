//go:build darwin || linux

package gateway_test

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
)

// TestPollMutationHTTP maps poll.create, poll.vote and poll.close to the
// native poll: each saved acknowledgement equals the native results, a vote
// replaces the actor's earlier vote, and close is reported closed.
func TestPollMutationHTTP(t *testing.T) {
	f := newReactionHTTPFixture(t)
	target := contractRoom()
	observer := newReplySignalObserver(t, f.socket, *target)
	create := pollMutationRequest("poll-http-create", core.PollCreate, target, "")
	headerPost(t, f, create)
	created := waitHTTPPinResult(t, f, create.RequestID, core.Accepted)
	message := next(t, observer, func(e *irc.MessageEvent) bool { return e.Kind == "poll" && e.ID == created.Poll.Poll.ID })
	native := nativePollEntry(t, f.socket, created.Poll.Poll.ID)
	assertPollMutationFacts(t, created, create, &native)
	if message.Message != native.Text || !reflect.DeepEqual(message.PollOptions, native.Options) || !message.PollClosesAt.Equal(native.ExpiresAt) || message.AccountID != native.AccountID {
		t.Fatal("poll creation observer lost metadata", message, native)
	}
	vote := pollMutationRequest("poll-http-vote", core.PollVote, target, native.ID)
	headerPost(t, f, vote)
	voted := waitHTTPPinResult(t, f, vote.RequestID, core.Accepted)
	assertPollMutationFacts(t, voted, vote, &nextPollMutationSignal(t, observer, native.ID).ChatEntry)
	if voted.Poll.Poll.Votes[0] != 0 || voted.Poll.Poll.Votes[1] != 1 || voted.Poll.Poll.From != "fixture-author" {
		t.Fatal("native vote aggregate", voted.Poll)
	}
	// Replacing one actor's vote must not add a second voter.
	vote.RequestID, vote.Choice = "poll-http-replace-vote", 1
	headerPost(t, f, vote)
	replaced := waitHTTPPinResult(t, f, vote.RequestID, core.Accepted)
	assertPollMutationFacts(t, replaced, vote, &nextPollMutationSignal(t, observer, native.ID).ChatEntry)
	if replaced.Poll.Poll.Votes[0] != 1 || replaced.Poll.Poll.Votes[1] != 0 {
		t.Fatal("vote replacement added voter", replaced.Poll)
	}
	closeRequest := pollMutationRequest("poll-http-close", core.PollClose, target, native.ID)
	if status, admission := reactionHTTPCall(t, f, f.cred.token(t, closeRequest, true)); status != http.StatusOK && status != http.StatusAccepted || admission.Result == nil {
		t.Fatalf("request-route close admission: %d %+v", status, admission)
	}
	closed := waitHTTPPinResult(t, f, closeRequest.RequestID, core.Accepted)
	assertPollMutationFacts(t, closed, closeRequest, &nextPollMutationSignal(t, observer, native.ID).ChatEntry)
	if !closed.Poll.Poll.Closed {
		t.Fatal("close ACK remained open")
	}
}
