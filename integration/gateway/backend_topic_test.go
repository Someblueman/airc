//go:build darwin || linux

package gateway_test

import (
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestBackendTopicMatchesNativeMetadata(t *testing.T) {
	socket := fixtureWithAdmin(t, true)
	provision := dial(t, socket, "fixture-author", fixtureAccount, true, true)
	_ = provision.Close()
	promoteRoomOperator(t, socket)
	c, _ := acceptanceJournalFixture(t, socket, fixtureAccount)
	c.Principals[0].Operations = append(c.Principals[0].Operations, core.TopicSet)
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
	topic := "shared topic 🙂"
	request := core.Request{Version: 1, RequestID: "shared-topic", Operation: core.TopicSet, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Topic: &topic}
	grant, outcome := replyAttempt(t, journal, b, identity, request)
	if outcome.Topic == nil {
		t.Fatalf("native topic acknowledgement missing: %+v", outcome)
	}
	job, err := journal.RecordTopicAcceptance(t.Context(), grant, outcome.Topic, outcome.ObservedAt, time.Now())
	if err != nil || job.Result.State != core.Accepted || !job.Result.Durable || job.Result.SendAttempts != 1 || job.Result.Topic == nil {
		t.Fatal("topic acceptance was not saved", job, err)
	}
	seen := queryNativeTopic(t, socket, request.Target.Name)
	ack := job.Result.Topic.Topic
	if seen.Target != *request.Target || seen.Topic != topic || seen.SetBy != "fixture-author" || seen.SetAt.IsZero() || seen.Target != ack.Target || seen.Topic != ack.Topic || seen.SetBy != ack.SetBy || !seen.SetAt.Equal(ack.SetAt) {
		t.Fatalf("native topic differs from durable acknowledgement: %+v ack=%+v", seen, ack)
	}
}
