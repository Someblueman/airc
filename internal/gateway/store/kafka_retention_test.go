//go:build darwin || linux

package store

import (
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestKafkaRetentionPreservesPendingCopiesReplayAndPermanentPins(t *testing.T) {
	c, identity := kafkaReadStoreFixture(t)
	c.Retry.LifetimeSeconds, c.Retry.TerminalRetentionSeconds = 10, 10
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	r := core.Request{Version: 1, RequestID: "read", Operation: core.ContextRead, Target: &c.Kafka[0].Route.Target, ParentID: "0123456789abcdef0123456789abcdef"}
	m := auth.AuthenticatedMessage{Identity: identity, Request: r, ExpiresAt: 1100, ReplayUntil: 1160}
	var records []KafkaRecord
	for _, owner := range []string{"owner", "second"} {
		p, err := s.ProvisionKafka(t.Context(), owner)
		if err != nil {
			t.Fatal(err)
		}
		reserved, err := s.ReserveKafka(t.Context(), owner, 23, p, m, time.Unix(1000, 0))
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, reserved.Record)
	}
	if _, err := s.RetainKafkaOutcome(t.Context(), records[0], []byte(`{"page":[],"cursor":"whole"}`), time.Unix(1001, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteKafkaPublication(t.Context(), records[0], 7, time.Unix(1002, 0)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	c.Retry.LifetimeSeconds, c.Retry.TerminalRetentionSeconds = 10, 100
	s, err = Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	counts, err := s.Maintain(t.Context(), time.Unix(1011, 0))
	if err != nil || counts.KafkaRecords != 0 || counts.KafkaIntents != 0 {
		t.Fatal(counts, err)
	}
	counts, err = s.Maintain(t.Context(), time.Unix(1012, 0))
	if err != nil || counts.KafkaRecords != 1 || counts.KafkaIntents != 0 {
		t.Fatal("stored record retention", counts, err)
	}
	counts, err = s.Maintain(t.Context(), time.Unix(1160, 0))
	if err != nil || counts.KafkaRecords != 0 || counts.KafkaIntents != 0 {
		t.Fatal("pending copy pruned", counts, err)
	}
	p, err := s.ProvisionKafka(t.Context(), "second")
	if err != nil {
		t.Fatal(err)
	}
	reserved, err := s.ReserveKafka(t.Context(), "second", 23, p, m, time.Unix(1160, 0))
	if err != nil {
		t.Fatal(err)
	}
	if outcome, err := s.KafkaOutcome(t.Context(), reserved.Record); err != nil || string(outcome) != `{"page":[],"cursor":"whole"}` {
		t.Fatal(string(outcome), err)
	}
	if _, err := s.CompleteKafkaPublication(t.Context(), reserved.Record, 7, time.Unix(1160, 0)); err != nil {
		t.Fatal(err)
	}
	counts, err = s.Maintain(t.Context(), time.Unix(1169, 0))
	if err != nil || counts.KafkaRecords != 0 || counts.KafkaIntents != 0 {
		t.Fatal(counts, err)
	}
	counts, err = s.Maintain(t.Context(), time.Unix(1170, 0))
	if err != nil || counts.KafkaRecords != 1 || counts.KafkaIntents != 1 {
		t.Fatal("completed shared intent retained", counts, err)
	}
	for _, owner := range []string{"owner", "second"} {
		p, err := s.ProvisionKafka(t.Context(), owner)
		if err != nil || p != (KafkaPosition{NextOffset: 24, LeaderEpoch: 7}) {
			t.Fatal("permanent pin changed", owner, p, err)
		}
	}
}

func TestKafkaRetentionInclusiveReplayAndRollback(t *testing.T) {
	c, identity := kafkaReadStoreFixture(t)
	c.Retry.LifetimeSeconds, c.Retry.TerminalRetentionSeconds = 10, 10
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := s.ProvisionKafka(t.Context(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	r := core.Request{Version: 1, RequestID: "read", Operation: core.ContextRead, Target: &c.Kafka[0].Route.Target, ParentID: "0123456789abcdef0123456789abcdef"}
	reserved, err := s.ReserveKafka(t.Context(), "owner", 23, p, auth.AuthenticatedMessage{Identity: identity, Request: r, ExpiresAt: 1100, ReplayUntil: 1160}, time.Unix(1000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RetainKafkaOutcome(t.Context(), reserved.Record, []byte(`{"page":[]}`), time.Unix(1001, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteKafkaPublication(t.Context(), reserved.Record, 7, time.Unix(1002, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), `CREATE TABLE fixture_parent(id INTEGER PRIMARY KEY);
CREATE TABLE fixture_child(id INTEGER REFERENCES fixture_parent(id) DEFERRABLE INITIALLY DEFERRED);
CREATE TRIGGER fixture_commit AFTER DELETE ON kafka_intents BEGIN INSERT INTO fixture_child VALUES(1); END;`); err != nil {
		t.Fatal(err)
	}
	counts, err := s.Maintain(t.Context(), time.Unix(1161, 0))
	if err == nil || counts != (Maintenance{}) {
		t.Fatal("failed commit claimed cleanup", counts, err)
	}
	if outcome, err := s.KafkaOutcome(t.Context(), reserved.Record); err != nil || string(outcome) != `{"page":[]}` {
		t.Fatal("failed cleanup lost record/outcome", string(outcome), err)
	}
	if _, err := s.db.ExecContext(t.Context(), "DROP TRIGGER fixture_commit"); err != nil {
		t.Fatal(err)
	}
	counts, err = s.Maintain(t.Context(), time.Unix(1160, 0))
	if err != nil || counts.KafkaRecords != 1 || counts.KafkaIntents != 0 {
		t.Fatal("inclusive replay boundary", counts, err)
	}
	counts, err = s.Maintain(t.Context(), time.Unix(1161, 0))
	if err != nil || counts.KafkaRecords != 0 || counts.KafkaIntents != 1 {
		t.Fatal("expired intent not reclaimed", counts, err)
	}
}
