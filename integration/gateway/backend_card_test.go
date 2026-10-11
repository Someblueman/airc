//go:build darwin || linux

package gateway_test

import (
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestBackendProfileAndPresenceMatchNativeCard(t *testing.T) {
	socket := fixture(t)
	provision := dial(t, socket, "fixture-author", fixtureAccount, true, true)
	_ = provision.Close()
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
	about := "shared profile 🙂"
	requests := []core.Request{
		{Version: 1, RequestID: "shared-profile", Operation: core.ProfileUpdate, Profile: &core.ProfilePatch{About: &about}},
		{Version: 1, RequestID: "shared-presence", Operation: core.PresenceSet, Presence: &core.PresenceInput{State: "away", Note: "shared presence", TTLSeconds: 30}},
	}
	account := ""
	for _, request := range requests {
		grant, outcome := replyAttempt(t, journal, b, identity, request)
		if outcome.Card == nil {
			t.Fatalf("native card acknowledgement missing: %+v", outcome)
		}
		job, err := journal.RecordCardAcceptance(t.Context(), grant, outcome.Card, outcome.ObservedAt, time.Now())
		if err != nil || job.Result.State != core.Accepted || !job.Result.Durable || job.Result.SendAttempts != 1 || job.Result.Card == nil {
			t.Fatal("card acceptance was not saved", job, err)
		}
		seen := nativeCard(t, socket, "fixture-author")
		ack := job.Result.Card.Card
		if seen.Nick != "fixture-author" || seen.AccountID == "" || seen.AccountID != ack.AccountID || seen.AgentProfile != ack.AgentProfile || seen.About != about {
			t.Fatalf("native card differs from durable acknowledgement: %+v ack=%+v", seen, ack)
		}
		if request.Operation == core.ProfileUpdate {
			account = seen.AccountID
		} else if seen.AccountID != account || seen.State != request.Presence.State || seen.Note != request.Presence.Note || seen.State != ack.State || seen.Note != ack.Note || !seen.ExpiresAt.Equal(ack.ExpiresAt) {
			t.Fatalf("presence changed account or native state: %+v ack=%+v", seen, ack)
		}
	}
}
