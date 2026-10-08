//go:build darwin || linux

package adapter

import (
	"bufio"
	"crypto/tls"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/kafkawire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestKafkaRecordsCorruptCacheCannotPublishOrReread(t *testing.T) {
	f := newKafkaAdmissionFixture(t)
	position, err := f.s.ProvisionKafka(t.Context(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	reads := kafkaNativeHistory(t, f, nil, "CHECK=1")
	request := core.Request{Version: 1, RequestID: "corrupt-cache", Operation: core.HistoryRead, Target: amqpRequest().Target, Limit: 1}
	record := kafkaSignedRecord(t, f, request, 23, 0)
	admission, err := reserveKafkaRecord(t.Context(), f.c.Kafka[0], f.d, record, position, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	bad := []byte(`{"version":2,"unexpected":"value"}`)
	if _, err := f.s.RetainKafkaOutcome(t.Context(), admission.reservation.Record, bad, time.Now()); err != nil {
		t.Fatal(err)
	}
	p, b := kafkaRecordsPair(t, f, func(_ *tls.Conn, r *bufio.Reader, _ config.Kafka) {
		if _, err := r.ReadByte(); err == nil {
			t.Error("corrupt cache published")
		}
	})
	sealer, _, _, _ := redisResultFixture(t)
	fetched := kafkawire.FetchResult{LogStartOffset: 0, LastStableOffset: 24, HighWatermark: 24, Batches: []kafkawire.Batch{{BaseOffset: 23, LastOffset: 23, LeaderEpoch: 8, Records: []kafkawire.Record{record}}}}
	next, err := p.processKafkaRange(t.Context(), f.c.Realm, b, f.d, f.s, sealer, make(chan struct{}, 1), position, fetched)
	if !errors.Is(err, kafkawire.ErrFrame) || next != position {
		t.Fatal("corrupt cache completed", next, err)
	}
	p.close()
	cache, err := f.s.KafkaOutcome(t.Context(), admission.reservation.Record)
	if err != nil || string(cache) != string(bad) || reads.Load() != 0 {
		t.Fatal("corrupt cache replaced or reread", err)
	}
}

func TestKafkaRecordsEmptyRangeRequiresNoPendingAssociation(t *testing.T) {
	for _, mode := range []string{"compacted", "pending", "watermark"} {
		t.Run(mode, func(t *testing.T) {
			f := newKafkaAdmissionFixture(t)
			position, err := f.s.ProvisionKafka(t.Context(), "owner")
			if err != nil {
				t.Fatal(err)
			}
			if mode == "pending" {
				request := amqpRequest()
				request.RequestID = "pending-association"
				if _, err := reserveKafkaRecord(t.Context(), f.c.Kafka[0], f.d, kafkaSignedRecord(t, f, request, 25, 0), position, time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			p, b := kafkaRecordsPair(t, f, func(_ *tls.Conn, r *bufio.Reader, _ config.Kafka) {
				if _, err := r.ReadByte(); err == nil {
					t.Error("empty range emitted a result")
				}
			})
			sealer, _, _, _ := redisResultFixture(t)
			fetched := kafkawire.FetchResult{LogStartOffset: 0, LastStableOffset: 100, HighWatermark: 100}
			if mode != "watermark" {
				fetched.Batches = []kafkawire.Batch{{BaseOffset: 23, LastOffset: 26, LeaderEpoch: 8}}
			}
			next, err := p.processKafkaRange(t.Context(), f.c.Realm, b, f.d, f.s, sealer, make(chan struct{}, 1), position, fetched)
			if mode == "compacted" {
				if err != nil || next != (store.KafkaPosition{NextOffset: 27, LeaderEpoch: 8}) {
					t.Fatal("proven compacted range not advanced", next, err)
				}
			} else if mode == "pending" {
				if !errors.Is(err, store.ErrConflict) || next != position {
					t.Fatal("pending association skipped", next, err)
				}
			} else if err != nil || next != position {
				t.Fatal("watermark inferred advancement", next, err)
			}
			p.close()
			if current, err := f.s.ProvisionKafka(t.Context(), "owner"); err != nil || current != next {
				t.Fatal("wrong saved range position", current, err)
			}
		})
	}
}
