//go:build darwin || linux

package store

import (
	"errors"
	"testing"
	"time"
)

func TestKafkaReservationRetainedRowCapsRollback(t *testing.T) {
	for _, which := range []string{"intents", "records"} {
		t.Run(which, func(t *testing.T) {
			c, identity := kafkaStoreFixture(t)
			s, err := Open(t.Context(), c)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			p, err := s.ProvisionKafka(t.Context(), "owner")
			if err != nil {
				t.Fatal(err)
			}
			count := kafkaRetainedRows
			if which == "records" {
				count = 1
			}
			_, err = s.db.ExecContext(t.Context(), `WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<?)
 INSERT INTO kafka_intents (principal,binding,request_id,fingerprint,request,operation,replay_until,admitted_at,retention_seconds)
 SELECT 'device','backend','retired-'||x,printf('%064x',x),x'7b7d','send',0,1000,60 FROM n`, count)
			if err != nil {
				t.Fatal(err)
			}
			if which == "records" {
				_, err = s.db.ExecContext(t.Context(), `WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<?)
 INSERT INTO kafka_records (namespace,topic,partition,offset,record_id,principal,binding,request_id,expected_position,admitted_at,retention_seconds,completed_at)
 SELECT 'history','events',2,x,printf('%064x',x),'device','backend','retired-1',0,1000,60,1001 FROM n`, kafkaRetainedRows)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.ReserveKafka(t.Context(), "owner", 20000, p, kafkaMessage(request(), identity), time.Unix(1000, 0)); !errors.Is(err, ErrCapacity) {
				t.Fatal("cap allowed intake", err)
			}
			var jobs, intents, records int
			if err := s.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM jobs),(SELECT count(*) FROM kafka_intents),(SELECT count(*) FROM kafka_records)`).Scan(&jobs, &intents, &records); err != nil {
				t.Fatal(err)
			}
			expectedRecords := 0
			if which == "records" {
				expectedRecords = kafkaRetainedRows
			}
			if jobs != 0 || intents != count || records != expectedRecords {
				t.Fatal("cap evicted or partially admitted", jobs, intents, records)
			}
			after, err := s.ProvisionKafka(t.Context(), "owner")
			if err != nil || after != p {
				t.Fatal("cap advanced position", after, err)
			}
		})
	}
}
