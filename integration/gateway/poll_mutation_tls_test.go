//go:build darwin || linux

package gateway_test

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestPollMutationOverFramedTLS(t *testing.T) {
	conn, credentials, stop, socket := framedTLSCardFixture(t)
	defer conn.Close()
	defer stop()
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	observer := newReplySignalObserver(t, socket, *target)
	call := func(request core.Request, correlation string) adapter.FrameResponse {
		writeFrame(t, conn, adapter.FrameRequest{Version: 1, CorrelationID: correlation, Profile: "encrypted", Envelope: credentials.token(t, request, true)})
		response := readFrame(t, conn)
		if response.CorrelationID != correlation || response.Event != "result" {
			t.Fatalf("TLS poll response mismatch: %+v", response)
		}
		return response
	}
	create := pollMutationRequest("poll-tls-create", core.PollCreate, target, "")
	create.Poll = &core.PollInput{Question: "TLS poll 🙂", Options: []string{"yes", "no"}, TTLSeconds: 60}
	if response := call(create, create.RequestID); response.Error != nil || response.Result == nil {
		t.Fatalf("TLS poll create admission: %+v", response)
	}
	created := waitPollMutationResult(t, call, create)
	createdNative := pollObserverEntry(t, observer, created.Poll.Poll.ID, false)
	assertPollMutationFacts(t, created, create, &createdNative)
	vote := pollMutationRequest("poll-tls-vote", core.PollVote, target, created.Poll.Poll.ID)
	vote.Choice = 2
	if response := call(vote, vote.RequestID); response.Error != nil || response.Result == nil {
		t.Fatalf("TLS poll vote admission: %+v", response)
	}
	voted := waitPollMutationResult(t, call, vote)
	votedNative := pollObserverEntry(t, observer, created.Poll.Poll.ID, true)
	assertPollMutationFacts(t, voted, vote, &votedNative)
	closeRequest := pollMutationRequest("poll-tls-close", core.PollClose, target, created.Poll.Poll.ID)
	if response := call(closeRequest, closeRequest.RequestID); response.Error != nil || response.Result == nil {
		t.Fatalf("TLS poll close admission: %+v", response)
	}
	closed := waitPollMutationResult(t, call, closeRequest)
	closedNative := pollObserverEntry(t, observer, created.Poll.Poll.ID, true)
	assertPollMutationFacts(t, closed, closeRequest, &closedNative)
}
