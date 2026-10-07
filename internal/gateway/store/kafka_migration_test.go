//go:build darwin || linux

package store

import (
	"testing"
	"time"
)

func TestKafkaMigrationPreservesJobsAndOSCORE(t *testing.T) {
	c, identity := kafkaStoreFixture(t)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	job, _, err := s.Admit(t.Context(), identity, request(), 0, time.Unix(1000, 0))
	if err != nil {
		t.Fatal(err)
	}
	key := oscoreKey()
	if err := s.ProvisionOSCORE(t.Context(), key); err != nil {
		t.Fatal(err)
	}
	if err := s.ReserveOSCORESender(t.Context(), key, 8); err != nil {
		t.Fatal(err)
	}
	before, err := s.LoadOSCORE(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), "DROP TABLE kafka_records; DROP TABLE kafka_intents; DROP TABLE kafka_positions; PRAGMA user_version=14"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	after, err := s.LoadOSCORE(t.Context(), key)
	if err != nil || after != before {
		t.Fatal("OSCORE changed", after, err)
	}
	retained, err := s.Lookup(t.Context(), identity, job.Request.RequestID)
	if err != nil || retained.UpstreamID != job.UpstreamID || retained.Request.Message != job.Request.Message || retained.Result.State != job.Result.State {
		t.Fatal("job changed", err)
	}
	for _, table := range []string{"kafka_positions", "kafka_intents", "kafka_records"} {
		var count int
		if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatal("migration invented Kafka state", table, count, err)
		}
	}
	var version int
	if err := s.db.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil || version != schemaVersion {
		t.Fatal("schema version", version, err)
	}
	if p, err := s.ProvisionKafka(t.Context(), "owner"); err != nil || p.NextOffset != 23 {
		t.Fatal("provision after migration", p, err)
	}
}
