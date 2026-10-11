//go:build darwin || linux

package gateway_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestBackendPollCreateVoteCloseMatchesNativeResults(t *testing.T) {
	socket := fixture(t)
	provision := dial(t, socket, "fixture-author", fixtureAccount, true, true)
	_ = provision.Close()
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	c, _ := acceptanceJournalFixture(t, socket, fixtureAccount)
	identity := fixtureIdentity(t, c)
	b, err := backend.New(c)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := store.Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	pollID := ""
	for _, op := range []core.Operation{core.PollCreate, core.PollVote, core.PollClose} {
		request := pollMutationRequest("shared-"+strings.ReplaceAll(string(op), ".", "-"), op, target, pollID)
		if op == core.PollVote {
			request.Choice = 1
		}
		grant, outcome := replyAttempt(t, journal, b, identity, request)
		if outcome.Poll == nil {
			t.Fatalf("native poll acknowledgement missing: %+v", outcome)
		}
		job, err := journal.RecordPollAcceptance(t.Context(), grant, outcome.Poll, outcome.ObservedAt, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		pollID = job.Result.Poll.Poll.ID
		native := nativePollEntry(t, socket, pollID)
		assertPollMutationFacts(t, job.Result, request, &native)
		votes := 0
		if op != core.PollCreate {
			votes = 1
		}
		if len(native.Votes) != 2 || native.Votes[0] != votes || native.Votes[1] != 0 || native.Closed != (op == core.PollClose) {
			t.Fatalf("native poll vote/close state differs: %+v", native)
		}
	}
}
