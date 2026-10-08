//go:build darwin || linux

package adapter

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/kafkawire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestKafkaRecordsReadFailureRemainsPending(t *testing.T) {
	f := newKafkaAdmissionFixture(t)
	position, err := f.s.ProvisionKafka(t.Context(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	reads := kafkaNativeHistory(t, f, nil, "CHECK=0")
	request := core.Request{Version: 1, RequestID: "read-failure", Operation: core.HistoryRead, Target: amqpRequest().Target, Limit: 1}
	record := kafkaSignedRecord(t, f, request, 23, 0)
	admission, err := reserveKafkaRecord(t.Context(), f.c.Kafka[0], f.d, record, position, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	produced := make(chan []byte, 1)
	p, b := kafkaRecordsPair(t, f, func(c *tls.Conn, r *bufio.Reader, b config.Kafka) {
		correlation, result := kafkaPeerProducedRecord(t, r, b)
		if len(result.Value) == 0 {
			return
		}
		produced <- result.Value
		_ = kafkaPeerReply(c, correlation, kafkaProduceSuccess(b))
		_, _ = r.ReadByte()
	})
	sealer, pub, _, _ := redisResultFixture(t)
	fetched := kafkawire.FetchResult{LogStartOffset: 0, LastStableOffset: 24, HighWatermark: 24, Batches: []kafkawire.Batch{{BaseOffset: 23, LastOffset: 23, LeaderEpoch: 8, Records: []kafkawire.Record{record}}}}
	next, err := p.processKafkaRange(t.Context(), f.c.Realm, b, f.d, f.s, sealer, make(chan struct{}, 1), position, fetched)
	if !errors.Is(err, backend.ErrReadUnsupported) || next != position {
		t.Fatal("failed read completed", next, err)
	}
	p.close()
	scope := kafkaResponseScope(f.c.Realm, b.Route.Principal, f.identity.Binding(), b, 23, request.RequestID)
	plain, err := auth.OpenSignedResponse(string(<-produced), "response", pub, scope, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var response KafkaResult
	if err := json.Unmarshal(plain, &response); err != nil {
		t.Fatal(err)
	}
	if response.Result != nil || response.Error == nil || response.Error.Code != core.UnsupportedOperation || response.Error.Transient {
		t.Fatal("wrong bounded failure", response)
	}
	if cache, err := f.s.KafkaOutcome(t.Context(), admission.reservation.Record); err != nil || cache != nil {
		t.Fatal("failure replaced a whole outcome", err)
	}
	if current, err := f.s.ProvisionKafka(t.Context(), "owner"); err != nil || current != position {
		t.Fatal("failed read advanced", current, err)
	}
	if reads.Load() != 0 {
		t.Fatal("unsupported native read executed")
	}
}

func TestKafkaRecordsParentDeadlineJoinsNativeRead(t *testing.T) {
	f := newKafkaAdmissionFixture(t)
	position, err := f.s.ProvisionKafka(t.Context(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", f.c.Bindings[0].Address)
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan net.Conn, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	defer func() {
		_ = listener.Close()
		<-done
		select {
		case conn := <-accepted:
			_ = conn.Close()
		default:
		}
	}()
	request := core.Request{Version: 1, RequestID: "cancelled-read", Operation: core.HistoryRead, Target: amqpRequest().Target, Limit: 1}
	record := kafkaSignedRecord(t, f, request, 23, 0)
	p, b := kafkaRecordsPair(t, f, func(_ *tls.Conn, r *bufio.Reader, _ config.Kafka) {
		if _, err := r.ReadByte(); err == nil {
			t.Error("cancelled read published")
		}
	})
	sealer, _, _, _ := redisResultFixture(t)
	fetched := kafkawire.FetchResult{LogStartOffset: 0, LastStableOffset: 24, HighWatermark: 24, Batches: []kafkawire.Batch{{BaseOffset: 23, LastOffset: 23, LeaderEpoch: 8, Records: []kafkawire.Record{record}}}}
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	slots := make(chan struct{}, 1)
	start := time.Now()
	next, err := p.processKafkaRange(ctx, f.c.Realm, b, f.d, f.s, sealer, slots, position, fetched)
	if err == nil || next != position || len(slots) != 0 || time.Since(start) > time.Second {
		t.Fatal("read did not stop within parent lease", next, err)
	}
	p.close()
	select {
	case conn := <-accepted:
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		if _, err := io.Copy(io.Discard, conn); err != nil {
			t.Fatal("native read retained", err)
		}
	case <-time.After(time.Second):
		t.Fatal("native read never started")
	}
	if _, err := f.s.Lookup(t.Context(), f.identity, request.RequestID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("read invented delivery job", err)
	}
	if current, err := f.s.ProvisionKafka(t.Context(), "owner"); err != nil || current != position {
		t.Fatal("cancelled read advanced", current, err)
	}
}
