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

func TestKafkaRecordsRefusalHasNoPublicationOrIntake(t *testing.T) {
	for _, mode := range []string{"bad-signature", "wrong-key", "wrong-target", "expired-read", "unknown-expired-send", "revoked", "shared-capacity", "stale-position", "bad-later-batch"} {
		t.Run(mode, func(t *testing.T) {
			f := newKafkaAdmissionFixture(t)
			position, err := f.s.ProvisionKafka(t.Context(), "owner")
			if err != nil {
				t.Fatal(err)
			}
			request := amqpRequest()
			request.RequestID = "refused-record"
			shift := int64(0)
			if mode == "wrong-target" {
				target := *request.Target
				target.Name = "#other"
				request.Target = &target
			}
			if mode == "unknown-expired-send" {
				shift = -400
			}
			if mode == "expired-read" {
				request.Operation = core.HistoryRead
				request.Message = ""
				request.Limit = 1
				shift = -400
			}
			record := kafkaSignedRecord(t, f, request, 23, shift)
			if mode == "wrong-key" {
				record.Key = []byte("different-id")
			}
			if mode == "bad-signature" {
				record.Value[0] = 'X'
			}
			slots := make(chan struct{}, 1)
			if mode == "shared-capacity" {
				slots <- struct{}{}
			}
			if mode == "revoked" {
				if _, err := f.s.Revoke(t.Context(), f.identity.ID()); err != nil {
					t.Fatal(err)
				}
			}
			peerDone := make(chan struct{})
			p, b := kafkaRecordsPair(t, f, func(_ *tls.Conn, r *bufio.Reader, _ config.Kafka) {
				defer close(peerDone)
				if _, err := r.ReadByte(); err == nil {
					t.Error("refusal emitted a result")
				}
			})
			fetched := kafkawire.FetchResult{LogStartOffset: 0, LastStableOffset: 26, HighWatermark: 26, Batches: []kafkawire.Batch{{BaseOffset: 23, LastOffset: 25, LeaderEpoch: 8, Records: []kafkawire.Record{record}}}}
			if mode == "bad-later-batch" {
				fetched.Batches = append(fetched.Batches, kafkawire.Batch{BaseOffset: 25, LastOffset: 25, LeaderEpoch: 8})
			}
			expected := position
			if mode == "stale-position" {
				expected.LeaderEpoch = 7
			}
			sealer, _, _, _ := redisResultFixture(t)
			next, err := p.processKafkaRange(t.Context(), f.c.Realm, b, f.d, f.s, sealer, slots, expected, fetched)
			if err == nil || next != expected {
				t.Fatal("refusal altered caller position", next, err)
			}
			p.close()
			<-peerDone
			if mode == "revoked" {
				if _, err := f.s.ProvisionKafka(t.Context(), "owner"); err == nil {
					t.Fatal("revoked owner retained authority")
				}
			} else {
				if current, err := f.s.ProvisionKafka(t.Context(), "owner"); err != nil || current != position {
					t.Fatal("refusal advanced", current, err)
				}
				if _, err := f.s.Lookup(t.Context(), f.identity, request.RequestID); !errors.Is(err, store.ErrNotFound) {
					t.Fatal("refusal admitted a job", err)
				}
			}
			want := 0
			if mode == "shared-capacity" {
				want = 1
			}
			if len(slots) != want {
				t.Fatal("shared request charge changed")
			}
		})
	}
}

func TestKafkaRecordsInvalidProduceReplyRetainsPendingOutcome(t *testing.T) {
	f := newKafkaAdmissionFixture(t)
	position, err := f.s.ProvisionKafka(t.Context(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	request := amqpRequest()
	request.RequestID = "invalid-ack"
	record := kafkaSignedRecord(t, f, request, 23, 0)
	p, b := kafkaRecordsPair(t, f, func(c *tls.Conn, r *bufio.Reader, b config.Kafka) {
		correlation, _ := kafkaPeerProducedRecord(t, r, b)
		reply := kafkaProduceSuccess(b)
		reply = append(reply, 0) // Trailing bytes invalidate the whole acknowledgement.
		_ = kafkaPeerReply(c, correlation, reply)
	})
	sealer, _, _, _ := redisResultFixture(t)
	fetched := kafkawire.FetchResult{LogStartOffset: 0, LastStableOffset: 24, HighWatermark: 24, Batches: []kafkawire.Batch{{BaseOffset: 23, LastOffset: 23, LeaderEpoch: 8, Records: []kafkawire.Record{record}}}}
	next, err := p.processKafkaRange(t.Context(), f.c.Realm, b, f.d, f.s, sealer, make(chan struct{}, 1), position, fetched)
	if !errors.Is(err, kafkawire.ErrFrame) || next != position {
		t.Fatal("malformed acknowledgement advanced", next, err)
	}
	p.close()
	admission, err := reserveKafkaRecord(t.Context(), b, f.d, record, position, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if cache, err := f.s.KafkaOutcome(t.Context(), admission.reservation.Record); err != nil || cache == nil {
		t.Fatal("whole outcome lost", err)
	}
	if current, err := f.s.ProvisionKafka(t.Context(), "owner"); err != nil || current != position {
		t.Fatal("invalid broker reply advanced", current, err)
	}
}
