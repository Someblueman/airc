//go:build darwin || linux

package store

import (
	"errors"
	"testing"
)

func TestKafkaPositionCapacityPreservesRetiredScopes(t *testing.T) {
	c, _ := kafkaStoreFixture(t)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProvisionKafka(t.Context(), "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), `WITH RECURSIVE retired(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM retired WHERE n<31)
 INSERT INTO kafka_positions SELECT 'retired'||n,topic,partition,cluster_id,principal,binding,target,result_topic,result_partition,next_offset,leader_epoch
 FROM kafka_positions,retired WHERE namespace='history'`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	c.Kafka[0].Namespace = "new-history"
	s, err = Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.ProvisionKafka(t.Context(), "owner"); !errors.Is(err, ErrCapacity) {
		t.Fatal("position cap ignored", err)
	}
	var count int
	if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM kafka_positions").Scan(&count); err != nil || count != 32 {
		t.Fatal("retired positions evicted", count, err)
	}
}
