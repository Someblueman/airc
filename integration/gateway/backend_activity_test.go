//go:build darwin || linux

package gateway_test

import (
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestBackendActivityMatchesNativeSignal(t *testing.T) {
	socket := fixture(t)
	provision := dial(t, socket, "fixture-author", fixtureAccount, true, true)
	_ = provision.Close()
	request := activityRequest("shared-activity", "typing", "shared activity", 5)
	observer := newActivityObserver(t, socket, *request.Target)
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
	grant, outcome := replyAttempt(t, journal, b, identity, request)
	if outcome.Activity == nil {
		t.Fatalf("native activity acknowledgement missing: %+v", outcome)
	}
	job, err := journal.RecordActivityAcceptance(t.Context(), grant, outcome.Activity, outcome.ObservedAt, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	assertActivityMutationFacts(t, job.Result, request, nextActivitySignal(t, observer))
}
