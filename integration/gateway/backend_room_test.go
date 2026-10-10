//go:build darwin || linux

package gateway_test

import (
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestBackendRoomUpdateMatchesNativeConfiguration(t *testing.T) {
	socket := fixtureWithAdmin(t, true)
	provision := dial(t, socket, "fixture-author", fixtureAccount, true, true)
	_ = provision.Close()
	promoteRoomOperator(t, socket)
	c, _ := acceptanceJournalFixture(t, socket, fixtureAccount)
	c.Principals[0].Operations = append(c.Principals[0].Operations, core.RoomUpdate)
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
	slow, limit := int64(1), 32
	request := roomUpdateRequest(t, "shared-room-update", &core.Target{Kind: "room", Name: "#gateway-fixture"}, &slow, &limit)
	grant, outcome := replyAttempt(t, journal, b, identity, request)
	if outcome.Room == nil {
		t.Fatalf("native room acknowledgement missing: %+v", outcome)
	}
	job, err := journal.RecordRoomAcceptance(t.Context(), grant, outcome.Room, outcome.ObservedAt, time.Now())
	if err != nil || job.Result.State != core.Accepted || !job.Result.Durable || job.Result.SendAttempts != 1 || job.Result.Room == nil {
		t.Fatal("room acceptance was not saved", job, err)
	}
	seen := queryNativeRoom(t, socket, request.Target.Name)
	ack := job.Result.Room.Room
	if seen.Target != *request.Target || seen.SlowSeconds != slow || seen.HistoryLimit != limit || seen != ack {
		t.Fatalf("native room differs from durable acknowledgement: %+v ack=%+v", seen, ack)
	}
}
