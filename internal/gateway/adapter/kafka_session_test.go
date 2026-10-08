//go:build darwin || linux

package adapter

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/kafkawire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func kafkaSessionFetch(b config.Kafka, batches []byte) []byte {
	wire := binary.BigEndian.AppendUint32(nil, 0)
	wire = binary.BigEndian.AppendUint16(wire, 0)
	for _, n := range []uint32{0, 1} {
		wire = binary.BigEndian.AppendUint32(wire, n)
	}
	wire = binary.BigEndian.AppendUint16(wire, uint16(len(b.Route.Topic)))
	wire = append(wire, b.Route.Topic...)
	for _, n := range []uint32{1, uint32(b.Route.Partition)} {
		wire = binary.BigEndian.AppendUint32(wire, n)
	}
	wire = binary.BigEndian.AppendUint16(wire, 0)
	for _, n := range []uint64{40, 40, 0} {
		wire = binary.BigEndian.AppendUint64(wire, n)
	}
	for _, n := range []uint32{0xffffffff, 0xffffffff, uint32(len(batches))} {
		wire = binary.BigEndian.AppendUint32(wire, n)
	}
	return append(wire, batches...)
}

func TestKafkaSessionWholeSendAndNextFetch(t *testing.T) {
	f := newKafkaAdmissionFixture(t)
	request := amqpRequest()
	request.RequestID = "session-send"
	record := kafkaSignedRecord(t, f, request, 23, 0)
	batch, err := kafkawire.RecordBatch(record.Key, record.Value, record.Timestamp)
	if err != nil {
		t.Fatal(err)
	}
	binary.BigEndian.PutUint64(batch, 23)
	// Base offset is outside the CRC-covered body.
	p, b := kafkaRecordsPair(t, f, func(c *tls.Conn, r *bufio.Reader, b config.Kafka) {
		want, _ := kafkawire.Fetch(b.Route.Topic, b.Route.Partition, 8, 23)
		api, v, corr, body, err := kafkaPeerRequest(r)
		if err != nil || api != 1 || v != 11 || !bytes.Equal(body, want) {
			t.Error("initial fetch", err)
			return
		}
		if kafkaPeerReply(c, corr, kafkaSessionFetch(b, batch)) != nil {
			return
		}
		corr, result := kafkaPeerProducedRecord(t, r, b)
		if string(result.Key) != request.RequestID {
			t.Error("result correlation")
			return
		}
		if kafkaPeerReply(c, corr, kafkaProduceSuccess(b)) != nil {
			return
		}
		want, _ = kafkawire.Fetch(b.Route.Topic, b.Route.Partition, 8, 24)
		api, v, _, body, err = kafkaPeerRequest(r)
		if err != nil || api != 1 || v != 11 || !bytes.Equal(body, want) {
			t.Error("next durable fetch", err)
		}
		// Closing the peer terminates this session after its verified progress.
	})
	sealer, _, _, _ := redisResultFixture(t)
	requests := make(chan struct{}, 1)
	if !runKafkaSession(t.Context(), p, f.c.Realm, b, f.d, f.s, sealer, requests) {
		t.Fatal("durable progress lost")
	}
	position, err := f.s.ProvisionKafka(t.Context(), b.ID)
	if err != nil || position != (store.KafkaPosition{NextOffset: 24, LeaderEpoch: 8}) || len(requests) != 0 {
		t.Fatal(position, err)
	}
}

func TestKafkaSessionContinuity(t *testing.T) {
	for _, end := range []int64{22, 23, 40} {
		t.Run(string(rune('a'+end)), func(t *testing.T) {
			f := newKafkaAdmissionFixture(t)
			p, b := kafkaRecordsPair(t, f, func(c *tls.Conn, r *bufio.Reader, b config.Kafka) {
				api, v, corr, body, err := kafkaPeerRequest(r)
				want, _ := kafkawire.EpochEnd(b.Route.Topic, b.Route.Partition, 8, 7)
				if err != nil || api != 23 || v != 3 || !bytes.Equal(body, want) {
					t.Error("epoch query", err)
					return
				}
				wire := binary.BigEndian.AppendUint32(nil, 0)
				wire = binary.BigEndian.AppendUint32(wire, 1)
				wire = binary.BigEndian.AppendUint16(wire, uint16(len(b.Route.Topic)))
				wire = append(wire, b.Route.Topic...)
				wire = binary.BigEndian.AppendUint32(wire, 1)
				wire = binary.BigEndian.AppendUint16(wire, 0)
				wire = binary.BigEndian.AppendUint32(wire, uint32(b.Route.Partition))
				wire = binary.BigEndian.AppendUint32(wire, 7)
				wire = binary.BigEndian.AppendUint64(wire, uint64(end))
				_ = kafkaPeerReply(c, corr, wire)
			})
			err := p.verifyKafkaContinuity(b, store.KafkaPosition{NextOffset: 23, LeaderEpoch: 7})
			if (err != nil) != (end < 23) {
				t.Fatal("wrong continuity", end, err)
			}
		})
	}
}

func TestKafkaSessionEmptyPollCancellation(t *testing.T) {
	f := newKafkaAdmissionFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	polled := make(chan struct{})
	p, b := kafkaRecordsPair(t, f, func(c *tls.Conn, r *bufio.Reader, b config.Kafka) {
		api, v, corr, _, err := kafkaPeerRequest(r)
		if err != nil || api != 1 || v != 11 {
			t.Error("empty poll", err)
			return
		}
		_ = kafkaPeerReply(c, corr, kafkaSessionFetch(b, nil))
		close(polled)
		_, _ = r.ReadByte()
	})
	sealer, _, _, _ := redisResultFixture(t)
	done := make(chan bool, 1)
	go func() { done <- runKafkaSession(ctx, p, f.c.Realm, b, f.d, f.s, sealer, make(chan struct{}, 1)) }()
	<-polled
	cancel()
	p.close()
	if <-done {
		t.Fatal("empty poll manufactured progress")
	}
	pos, err := f.s.ProvisionKafka(t.Context(), b.ID)
	if err != nil || pos != (store.KafkaPosition{NextOffset: 23, LeaderEpoch: -1}) {
		t.Fatal(pos, err)
	}
}
