//go:build darwin || linux

package adapter

import (
	"bufio"
	"crypto/tls"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/kafkawire"
)

func TestKafkaRecordsCachedReadUsesSignedReplayDeadline(t *testing.T) {
	f := newKafkaAdmissionFixture(t)
	position, err := f.s.ProvisionKafka(t.Context(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	// Build the authenticated socket pair before the short remaining lease starts.
	produced := make(chan struct{}, 1)
	p, b := kafkaRecordsPair(t, f, func(_ *tls.Conn, r *bufio.Reader, b config.Kafka) {
		_, result := kafkaPeerProducedRecord(t, r, b)
		if len(result.Value) == 0 {
			return
		}
		produced <- struct{}{}
		_, _ = r.ReadByte() // No Produce reply: the original read lease must stop the wait.
	})
	request := core.Request{Version: 1, RequestID: "read-lease", Operation: core.HistoryRead, Target: amqpRequest().Target, Limit: 1}
	record := kafkaSignedRecord(t, f, request, 23, -358)
	admission, err := reserveKafkaRecord(t.Context(), b, f.d, record, position, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	deadline := admission.verified.ReplayDeadline()
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > 2*time.Second {
		t.Fatal("invalid short-lease fixture", remaining)
	}
	cache, err := encodeKafkaOutcome(kafkaOutcome{Version: 1, RequestID: request.RequestID, Operation: request.Operation, Result: &dispatch.DetachedResult{Version: 1, Operation: request.Operation, Page: &backend.HistoryPage{Status: "ok"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.RetainKafkaOutcome(t.Context(), admission.reservation.Record, cache, time.Now()); err != nil {
		t.Fatal(err)
	}
	sealer, _, _, _ := redisResultFixture(t)
	fetched := kafkawire.FetchResult{LogStartOffset: 0, LastStableOffset: 24, HighWatermark: 24, Batches: []kafkawire.Batch{{BaseOffset: 23, LastOffset: 23, LeaderEpoch: 8, Records: []kafkawire.Record{record}}}}
	slots := make(chan struct{}, 1)
	next, err := p.processKafkaRange(t.Context(), f.c.Realm, b, f.d, f.s, sealer, slots, position, fetched)
	if err == nil || next != position || len(slots) != 0 {
		t.Fatal("expired lease completed", next, err)
	}
	if time.Now().After(deadline.Add(500 * time.Millisecond)) {
		t.Fatal("cached read used a fresh processing lease")
	}
	p.close()
	select {
	case <-produced:
	default:
		t.Fatal("fixture never reached cached publication")
	}
	if _, err := reserveKafkaRecord(t.Context(), b, f.d, record, position, deadline); !errors.Is(err, auth.ErrAuthentication) {
		t.Fatal("exact expired read boundary reserved", err)
	}
}
