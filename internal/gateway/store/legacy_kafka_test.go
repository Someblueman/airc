//go:build darwin || linux

package store

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// These SQL fixtures exercise the retained on-disk contract without restoring
// the removed Kafka configuration, consumer or store APIs.
func seedLegacyKafkaPosition(t *testing.T, s *Store) {
	t.Helper()
	_, err := s.db.ExecContext(t.Context(), `INSERT INTO kafka_positions VALUES
('history','events',2,'cluster','device','backend',?,'results',3,23,4)`, []byte(`{"kind":"room","name":"#Allowed"}`))
	if err != nil {
		t.Fatal(err)
	}
}

func checkLegacyKafkaPosition(t *testing.T, s *Store) {
	t.Helper()
	var position, epoch int64
	err := s.db.QueryRowContext(t.Context(), `SELECT next_offset,leader_epoch FROM kafka_positions
WHERE namespace='history' AND topic='events' AND partition=2`).Scan(&position, &epoch)
	if err != nil || position != 23 || epoch != 4 {
		t.Fatal("changed historical broker position", position, epoch, err)
	}
}

func TestRemovedKafkaPreservesJournalEvidence(t *testing.T) {
	c, identity, _ := fixture(t)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	now := time.Unix(1000, 0)
	job, _, err := s.Admit(t.Context(), identity, request(), 0, now)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := s.Reserve(t.Context(), identity.ID(), job.Request.RequestID, SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := grant.Consume(now)
	if err != nil {
		t.Fatal(err)
	}
	job, err = s.RecordAcceptance(t.Context(), grant, receiptFor(attempt), now, now)
	if err != nil {
		t.Fatal(err)
	}
	seedLegacyKafkaPosition(t, s)
	body, err := json.Marshal(job.Request)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := job.Request.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	outcome := []byte(`{"accepted":true}`)
	_, err = s.db.ExecContext(t.Context(), `INSERT INTO kafka_intents VALUES
('device','backend',?,?,?,'send',1100,1000,60,?,1000)`, job.Request.RequestID, fingerprint, body, outcome)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.ExecContext(t.Context(), `INSERT INTO kafka_records VALUES
('history','events',2,23,?,'device','backend',?,23,1000,60,NULL)`, strings.Repeat("a", 64), job.Request.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	// Maintenance must not erase old publication evidence or its accepted job,
	// even after the ordinary terminal-retention window has elapsed.
	if _, err := s.Maintain(t.Context(), now.Add(365*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	checkLegacyKafkaPosition(t, s)
	after, err := s.Lookup(t.Context(), identity, job.Request.RequestID)
	if err != nil || !reflect.DeepEqual(after, job) {
		t.Fatal("changed retained delivery", after, err)
	}
	var retainedBody, retainedOutcome []byte
	err = s.db.QueryRowContext(t.Context(), "SELECT request,outcome FROM kafka_intents").Scan(&retainedBody, &retainedOutcome)
	if err != nil || string(retainedBody) != string(body) || string(retainedOutcome) != string(outcome) {
		t.Fatal("changed historical intent", err)
	}
	var record string
	err = s.db.QueryRowContext(t.Context(), "SELECT record_id FROM kafka_records WHERE completed_at IS NULL").Scan(&record)
	if err != nil || record != strings.Repeat("a", 64) {
		t.Fatal("changed historical publication", record, err)
	}
}
