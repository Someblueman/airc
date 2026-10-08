//go:build darwin || linux

package adapter

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/kafkawire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestKafkaRecordsRetainPublishThenAdvance(t *testing.T) {
	f := newKafkaAdmissionFixture(t)
	position, err := f.s.ProvisionKafka(t.Context(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	request := amqpRequest()
	request.RequestID = "original-kafka-send"
	record := kafkaSignedRecord(t, f, request, 25, 0)
	produced := make(chan kafkawire.Record, 1)
	peerDone := make(chan struct{})
	p, b := kafkaRecordsPair(t, f, func(c *tls.Conn, r *bufio.Reader, b config.Kafka) {
		defer close(peerDone)
		correlation, result := kafkaPeerProducedRecord(t, r, b)
		if len(result.Value) == 0 {
			return
		}
		admission, err := reserveKafkaRecord(t.Context(), b, f.d, record, position, time.Now())
		if err != nil {
			t.Error(err)
			return
		}
		cached, err := f.s.KafkaOutcome(t.Context(), admission.reservation.Record)
		if err != nil || cached == nil {
			t.Error("publication preceded durable outcome", err)
			return
		}
		outcome, err := decodeKafkaOutcome(cached, request.RequestID, core.Send)
		if err != nil || outcome.Result.Result.Accepted != nil {
			t.Error("wrong cached acceptance", err)
			return
		}
		if current, err := f.s.ProvisionKafka(t.Context(), "owner"); err != nil || current != position {
			t.Error("position advanced before broker reply", current, err)
			return
		}
		produced <- result
		_ = kafkaPeerReply(c, correlation, kafkaProduceSuccess(b))
		_, _ = r.ReadByte()
	})
	sealer, pub, _, _ := redisResultFixture(t)
	slots := make(chan struct{}, 1)
	fetched := kafkawire.FetchResult{LogStartOffset: 0, LastStableOffset: 30, HighWatermark: 30, Batches: []kafkawire.Batch{
		{BaseOffset: 23, LastOffset: 25, LeaderEpoch: 8, Records: []kafkawire.Record{record}},
		{BaseOffset: 26, LastOffset: 29, LeaderEpoch: 8},
	}}
	next, err := p.processKafkaRange(t.Context(), f.c.Realm, b, f.d, f.s, sealer, slots, position, fetched)
	if err != nil || next != (store.KafkaPosition{NextOffset: 30, LeaderEpoch: 8}) || len(slots) != 0 {
		t.Fatal("wrong completion", next, err)
	}
	p.close()
	<-peerDone
	result := <-produced
	if string(result.Key) != request.RequestID {
		t.Fatal("result key rewritten")
	}
	scope := kafkaResponseScope(f.c.Realm, b.Route.Principal, f.identity.Binding(), b, 25, request.RequestID)
	plain, err := auth.OpenSignedResponse(string(result.Value), "response", pub, scope, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var response KafkaResult
	if err := json.Unmarshal(plain, &response); err != nil {
		t.Fatal(err)
	}
	if response.RequestID != request.RequestID || response.Offset != 25 || response.Result == nil || response.Result.Result == nil || !response.Result.Result.Durable || response.Result.Result.Accepted != nil || response.Result.Result.State != core.Queued {
		t.Fatal("native acceptance invented", response)
	}
	job, err := f.s.Lookup(t.Context(), f.identity, request.RequestID)
	if err != nil || job.Request.Message != request.Message || job.Request.RequestID != request.RequestID {
		t.Fatal("original intent changed", job, err)
	}
	if current, err := f.s.ProvisionKafka(t.Context(), "owner"); err != nil || current != next {
		t.Fatal("position not persisted", current, err)
	}
}

func TestKafkaRecordsStopAtFirstUnacceptedIntent(t *testing.T) {
	f := newKafkaAdmissionFixture(t)
	position, err := f.s.ProvisionKafka(t.Context(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	first := amqpRequest()
	first.RequestID = "first"
	later := first
	later.RequestID = "later"
	peerDone := make(chan struct{})
	p, b := kafkaRecordsPair(t, f, func(c *tls.Conn, r *bufio.Reader, b config.Kafka) {
		defer close(peerDone)
		correlation, record := kafkaPeerProducedRecord(t, r, b)
		if string(record.Key) != "first" {
			t.Error("wrong completed prefix")
			return
		}
		_ = kafkaPeerReply(c, correlation, kafkaProduceSuccess(b))
		if _, err := r.ReadByte(); err == nil {
			t.Error("later record published past refusal")
		}
	})
	sealer, _, _, _ := redisResultFixture(t)
	fetched := kafkawire.FetchResult{LogStartOffset: 0, LastStableOffset: 30, HighWatermark: 30, Batches: []kafkawire.Batch{{BaseOffset: 23, LastOffset: 29, LeaderEpoch: 8, Records: []kafkawire.Record{
		kafkaSignedRecord(t, f, first, 23, 0),
		{Offset: 24, Timestamp: 1000, Key: []byte("poison"), Value: []byte("not-a-JWS")},
		kafkaSignedRecord(t, f, later, 25, 0),
	}}}}
	next, err := p.processKafkaRange(t.Context(), f.c.Realm, b, f.d, f.s, sealer, make(chan struct{}, 1), position, fetched)
	if err == nil || next != (store.KafkaPosition{NextOffset: 24, LeaderEpoch: 8}) {
		t.Fatal("refusal skipped", next, err)
	}
	p.close()
	<-peerDone
	if _, err := f.s.Lookup(t.Context(), f.identity, later.RequestID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("later intent admitted", err)
	}
	if current, err := f.s.ProvisionKafka(t.Context(), "owner"); err != nil || current != next {
		t.Fatal("compacted suffix advanced past refusal", current, err)
	}
}
