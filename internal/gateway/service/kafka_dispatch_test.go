//go:build darwin || linux

package service

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/kafkawire"
	"github.com/Someblueman/airc/internal/gateway/natswire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestKafkaServiceSignedDispatchAndCommittedPosition(t *testing.T) {
	c, cert, signer, pub := natsServiceConfig(t)
	kafka, _ := kafkaServiceConfig(t)
	// Retain this fixture's signing identity and TLS roots.
	c.Listeners = kafka.Listeners
	c.Kafka = kafka.Kafka
	c.Kafka[0].Brokers[0].TLSCAFile = cert.CAPath
	request, wire := natsServicePublication(t, c, signer)
	frame, err := natswire.Read(bufio.NewReader(bytes.NewReader(wire)))
	if err != nil {
		t.Fatal(err)
	}
	batch, err := kafkawire.RecordBatch([]byte(request.RequestID), frame.Payload, time.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	binary.BigEndian.PutUint64(batch, 23)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	c.Kafka[0].Brokers[0].Address = listener.Addr().String()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	produced := make(chan []byte, 1)
	nextFetch, peerDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(peerDone)
		raw, err := listener.Accept()
		if err != nil {
			return
		}
		defer raw.Close()
		conn := tls.Server(raw, cert.Server)
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		if !kafkaServiceAuth(t, conn) {
			return
		}
		api, v, corr, _, err := kafkaServiceRequest(t, conn)
		if err != nil || api != 3 || v != 8 {
			t.Error("metadata", err)
			return
		}
		if kafkaServiceReply(conn, corr, kafkaServiceMetadataWire(c.Kafka[0], 7, 7, 8)) != nil {
			return
		}
		api, v, corr, body, err := kafkaServiceRequest(t, conn)
		want, _ := kafkawire.Fetch("events", 2, 8, 23)
		if err != nil || api != 1 || v != 11 || !bytes.Equal(body, want) {
			t.Error("Fetch", err)
			return
		}
		if kafkaServiceReply(conn, corr, kafkaServiceFetchReply("events", 2, batch)) != nil {
			return
		}
		api, v, corr, body, err = kafkaServiceRequest(t, conn)
		prefix := 26 + len("results")
		if err != nil || api != 0 || v != 7 || len(body) < prefix || binary.BigEndian.Uint16(body[2:]) != 65535 || string(body[14:21]) != "results" || binary.BigEndian.Uint32(body[prefix-8:]) != 3 {
			t.Error("Produce", err)
			return
		}
		batches, err := kafkawire.Records(body[prefix:])
		if err != nil || len(batches) != 1 || len(batches[0].Records) != 1 || string(batches[0].Records[0].Key) != request.RequestID {
			t.Error("whole result", err)
			return
		}
		produced <- batches[0].Records[0].Value
		if kafkaServiceReply(conn, corr, kafkaServiceProduceReply("results", 3)) != nil {
			return
		}
		api, v, _, body, err = kafkaServiceRequest(t, conn)
		want, _ = kafkawire.Fetch("events", 2, 8, 24)
		if err != nil || api != 1 || v != 11 || !bytes.Equal(body, want) {
			t.Error("committed Fetch", err)
			return
		}
		close(nextFetch)
		var b [1]byte
		_, _ = conn.Read(b[:])
	}()
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() { defer close(finished); err := Run(ctx, c, writer); _ = writer.CloseWithError(err); done <- err }()
	t.Cleanup(func() { cancel(); _ = listener.Close(); _ = reader.Close(); <-finished; <-peerDone })
	var ready json.RawMessage
	if err := json.NewDecoder(reader).Decode(&ready); err != nil {
		t.Fatal(err)
	}
	select {
	case <-nextFetch:
	case <-time.After(3 * time.Second):
		t.Fatal("signed dispatch failed")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	<-peerDone
	journal, err := store.Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	pos, err := journal.ProvisionKafka(t.Context(), "kafka")
	if err != nil || pos != (store.KafkaPosition{NextOffset: 24, LeaderEpoch: 8}) {
		t.Fatal(pos, err)
	}
	verifyKafkaServiceResult(t, c, pub, request, <-produced)
	verifyKafkaServiceJob(t, c, journal, request, string(frame.Payload))
}
