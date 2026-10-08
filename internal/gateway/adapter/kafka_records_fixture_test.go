//go:build darwin || linux

package adapter

import (
	"bufio"
	"crypto/tls"
	"encoding/binary"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/kafkawire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// This is a scripted TLS/SASL/metadata/Produce peer, not broker qualification.
func kafkaRecordsPair(t *testing.T, f amqpAdmissionFixture, produce func(*tls.Conn, *bufio.Reader, config.Kafka)) (*kafkaPair, config.Kafka) {
	t.Helper()
	b := f.c.Kafka[0]
	ready := make(chan struct{})
	rb, security, _ := redisFixture(t, func(_ int, c *tls.Conn, r *bufio.Reader) {
		<-ready
		if !kafkaPeerAuth(t, c, r, "", 0) {
			return
		}
		api, version, correlation, _, err := kafkaPeerRequest(r)
		if err != nil || api != 3 || version != 8 || correlation != 3 {
			t.Error("missing pinned metadata", err)
			return
		}
		if err := kafkaPeerReply(c, correlation, kafkaPairMetadataWire(b, 7, 7, 8)); err != nil {
			return
		}
		produce(c, r, b)
	})
	b.Brokers = []config.KafkaBroker{{ID: 7, Address: rb.Address, TLSServerName: rb.TLSServerName}}
	b.Credential.Username = "user"
	close(ready)
	p, err := openKafkaPair(t.Context(), b, map[int32]*tls.Config{7: security}, []byte("password"), make(chan struct{}, 1), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.close)
	return p, b
}

func kafkaPeerProducedRecord(t *testing.T, r *bufio.Reader, b config.Kafka) (int32, kafkawire.Record) {
	t.Helper()
	api, version, correlation, body, err := kafkaPeerRequest(r)
	if err != nil || api != 0 || version != 7 {
		t.Error("missing Produce", err)
		return 0, kafkawire.Record{}
	}
	// Produce v7's fixed route header precedes its one magic-2 record batch.
	prefix := 26 + len(b.Route.ResultTopic)
	if len(body) < prefix || binary.BigEndian.Uint16(body[:2]) != 65535 || binary.BigEndian.Uint16(body[2:4]) != 65535 || binary.BigEndian.Uint32(body[4:8]) != 1000 || binary.BigEndian.Uint32(body[8:12]) != 1 || int(binary.BigEndian.Uint16(body[12:14])) != len(b.Route.ResultTopic) || string(body[14:14+len(b.Route.ResultTopic)]) != b.Route.ResultTopic {
		t.Error("wrong all-replica Produce header")
		return 0, kafkawire.Record{}
	}
	// The header is 14 bytes plus topic, then partition count/index/batch length.
	if len(body) < prefix || binary.BigEndian.Uint32(body[prefix-12:]) != 1 || int32(binary.BigEndian.Uint32(body[prefix-8:])) != b.Route.ResultPartition || int(binary.BigEndian.Uint32(body[prefix-4:])) != len(body)-prefix {
		t.Error("wrong Produce partition")
		return 0, kafkawire.Record{}
	}
	batches, err := kafkawire.Records(body[prefix:])
	if err != nil || len(batches) != 1 || len(batches[0].Records) != 1 {
		t.Error("invalid whole result batch", err)
		return 0, kafkawire.Record{}
	}
	return correlation, batches[0].Records[0]
}

func kafkaProduceSuccess(b config.Kafka) []byte {
	wire := binary.BigEndian.AppendUint32(nil, 1)
	wire = binary.BigEndian.AppendUint16(wire, uint16(len(b.Route.ResultTopic)))
	wire = append(wire, b.Route.ResultTopic...)
	wire = binary.BigEndian.AppendUint32(wire, 1)
	wire = binary.BigEndian.AppendUint32(wire, uint32(b.Route.ResultPartition))
	wire = binary.BigEndian.AppendUint16(wire, 0)
	for _, n := range []uint64{100, ^uint64(0), 0} {
		wire = binary.BigEndian.AppendUint64(wire, n)
	}
	return binary.BigEndian.AppendUint32(wire, 0)
}

func kafkaSignedRecord(t *testing.T, f amqpAdmissionFixture, r core.Request, offset int64, shift int64) kafkawire.Record {
	t.Helper()
	return kafkawire.Record{Offset: offset, Timestamp: time.Now().UnixMilli(), Key: []byte(r.RequestID), Value: redisSignedRequest(t, f, r, shift)}
}

func reopenKafkaRecords(t *testing.T, f *amqpAdmissionFixture) {
	t.Helper()
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	f.s, err = store.Open(t.Context(), f.c)
	if err != nil {
		t.Fatal(err)
	}
	reopened := f.s
	t.Cleanup(func() { _ = reopened.Close() })
	f.d, err = dispatch.New(f.s, f.v, f.reader, f.cursors, f.route().Operations)
	if err != nil {
		t.Fatal(err)
	}
}
