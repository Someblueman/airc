//go:build darwin || linux

package gateway_test

import (
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestPollMutationHTTPAndNativeReplay(t *testing.T) {
	f := newReactionTransportFixture(t)
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	observer := newReplySignalObserver(t, f.socket, *target)
	create := pollMutationRequest("poll-http-create", core.PollCreate, target, "")
	roomBearerPost(t, f, create, 1)
	created := waitHTTPPinResult(t, f, create.RequestID, core.Accepted)
	message := next(t, observer, func(e *irc.MessageEvent) bool { return e.Kind == "poll" && e.ID == created.Poll.Poll.ID })
	native := nativePollEntry(t, f.socket, created.Poll.Poll.ID)
	assertPollMutationFacts(t, created, create, &native)
	if message.Message != native.Text || !reflect.DeepEqual(message.PollOptions, native.Options) || !message.PollClosesAt.Equal(native.ExpiresAt) || message.AccountID != native.AccountID {
		t.Fatal("poll creation observer lost metadata", message, native)
	}
	h2 := *f
	transport := f.http.Transport.(*http.Transport).Clone()
	transport.ForceAttemptHTTP2 = true
	t.Cleanup(transport.CloseIdleConnections)
	h2.http = &http.Client{Transport: transport, Timeout: f.http.Timeout}
	vote := pollMutationRequest("poll-http-vote", core.PollVote, target, native.ID)
	roomBearerPost(t, &h2, vote, 2)
	voted := waitHTTPPinResult(t, &h2, vote.RequestID, core.Accepted)
	signal := nextPollMutationSignal(t, observer, native.ID)
	assertPollMutationFacts(t, voted, vote, &signal.ChatEntry)
	if voted.Poll.Poll.Votes[0] != 0 || voted.Poll.Poll.Votes[1] != 1 || voted.Poll.Poll.From != "fixture-author" {
		t.Fatal("native vote aggregate", voted.Poll)
	}
	// Replacing one actor's vote must not add a second voter.
	vote.RequestID, vote.Choice = "poll-http-replace-vote", 1
	roomBearerPost(t, f, vote, 1)
	replaced := waitHTTPPinResult(t, f, vote.RequestID, core.Accepted)
	signal = nextPollMutationSignal(t, observer, native.ID)
	assertPollMutationFacts(t, replaced, vote, &signal.ChatEntry)
	if replaced.Poll.Poll.Votes[0] != 1 || replaced.Poll.Poll.Votes[1] != 0 {
		t.Fatal("vote replacement added voter", replaced.Poll)
	}
	closeRequest := pollMutationRequest("poll-http-close", core.PollClose, target, native.ID)
	issued, expires := time.Now().Unix(), time.Now().Unix()+120
	closeRequest.IssuedAt, closeRequest.ExpiresAt = &issued, &expires
	token := f.cred.token(t, closeRequest, false)
	status, admission := contextHTTPCall(t, &h2, token, false)
	if status != http.StatusOK && status != http.StatusAccepted || admission.Result == nil {
		t.Fatalf("signed close admission: %d %+v", status, admission)
	}
	closed := waitHTTPPinResult(t, &h2, closeRequest.RequestID, core.Accepted)
	signal = nextPollMutationSignal(t, observer, native.ID)
	assertPollMutationFacts(t, closed, closeRequest, &signal.ChatEntry)
	if !closed.Poll.Poll.Closed {
		t.Fatal("close ACK remained open")
	}
	// Replaying any historical mutation must preserve its original snapshot.
	for _, saved := range []struct {
		request core.Request
		result  core.Result
	}{{create, created}, {vote, replaced}, {closeRequest, closed}} {
		roomBearerPost(t, f, saved.request, 1)
		replay := waitHTTPPinResult(t, f, saved.request.RequestID, core.Accepted)
		if !reflect.DeepEqual(replay, saved.result) {
			t.Fatalf("poll replay changed historical evidence: %+v %+v", replay, saved.result)
		}
	}
	assertNoPollMutation(t, observer, native.ID)
}

func TestPollMutationEncryptedHTTPFramedAndWebSocket(t *testing.T) {
	f := newReactionTransportFixture(t)
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	observer := newReplySignalObserver(t, f.socket, *target)
	for _, carrier := range []struct {
		name string
		call func(string) (int, dispatch.DetachedResult)
	}{
		{"http", func(token string) (int, dispatch.DetachedResult) { return reactionHTTPCall(t, f, token) }},
		{"framed", func(token string) (int, dispatch.DetachedResult) {
			return reactionFramedCall(t, f.framed, token, "poll-frame")
		}},
		{"websocket", func(token string) (int, dispatch.DetachedResult) {
			return reactionWebSocketCall(t, f.ws, token, "poll-ws")
		}},
	} {
		id := ""
		for _, op := range []core.Operation{core.PollCreate, core.PollVote, core.PollClose} {
			r := pollMutationRequest("poll-"+carrier.name+"-"+map[core.Operation]string{core.PollCreate: "create", core.PollVote: "vote", core.PollClose: "close"}[op], op, target, id)
			status, admission := carrier.call(f.cred.token(t, r, true))
			if status != http.StatusOK && status != http.StatusAccepted || admission.Result == nil || !admission.Result.Durable {
				t.Fatalf("%s %s admission: %d %+v", carrier.name, op, status, admission)
			}
			result := waitHTTPPinResult(t, f, r.RequestID, core.Accepted)
			if op == core.PollCreate {
				id = result.Poll.Poll.ID
				next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == id && e.Kind == "poll" })
				entry := nativePollEntry(t, f.socket, id)
				assertPollMutationFacts(t, result, r, &entry)
			} else {
				signal := nextPollMutationSignal(t, observer, id)
				assertPollMutationFacts(t, result, r, &signal.ChatEntry)
			}
		}
	}
}
