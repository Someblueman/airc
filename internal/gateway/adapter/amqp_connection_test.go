//go:build darwin || linux

package adapter

import (
	"context"
	"crypto/tls"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/amqpwire"
	"github.com/Someblueman/airc/internal/gateway/config"
)

func amqpOwnedFixture(t *testing.T) (*amqpConnection, *tls.Conn, context.CancelFunc) {
	t.Helper()
	v, token := amqpVerifierFixture(t)
	client, server := amqpTLSPair(t, tls.VersionTLS13)
	ctx, cancel := context.WithCancel(t.Context())
	type result struct {
		c   *amqpConnection
		err error
	}
	done := make(chan result, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		c, err := openAMQPConnection(ctx, server, v, []config.AMQPRoute{{Principal: "producer"}}, "gateway-test")
		done <- result{c, err}
	}()
	t.Cleanup(func() { cancel(); closeMQTTConnection(client); closeMQTTConnection(server); <-finished })
	if err := client.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := client.HandshakeContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	amqpClientMechanisms(t, client)
	if err := writeAMQPSASL(client, amqpwire.SASLPerformative{Kind: 0x41, Fields: []amqpwire.Value{{Code: 0xa3, Data: []byte("PLAIN")}, {Code: 0xa0, Data: []byte("\x00producer\x00" + token)}}}); err != nil {
		t.Fatal(err)
	}
	p, err := readAMQPSASL(client)
	if err != nil || p.Kind != 0x44 || p.Fields[0].Number != 0 {
		t.Fatal(p, err)
	}
	amqpClientHeader(t, client, amqpwire.AMQPProtocol)
	write := func(p amqpwire.Performative) {
		f, err := amqpwire.EncodePerformative(p, 0, 512)
		if err != nil {
			t.Fatal(err)
		}
		if err := amqpwire.WriteFrame(client, f, 512); err != nil {
			t.Fatal(err)
		}
	}
	read := func() {
		f, err := amqpwire.ReadFrame(client, 512)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := amqpwire.DecodePerformative(f); err != nil {
			t.Fatal(err)
		}
	}
	write(amqpwire.Performative{Kind: 0x10, Fields: []amqpwire.Value{{Code: 0xa1, Data: []byte("client")}}})
	read()
	write(amqpwire.Performative{Kind: 0x11, Fields: []amqpwire.Value{{Code: 0x40}, {Code: 0x70, Number: 7}, {Code: 0x70, Number: 8}, {Code: 0x70, Number: 8}}})
	read()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatal(r.err)
		}
		t.Cleanup(r.c.close)
		return r.c, client, cancel
	case <-time.After(time.Second):
		t.Fatal("owner did not negotiate")
		return nil, nil, nil
	}
}

func TestAMQPConnectionHeartbeatAndJoinedClose(t *testing.T) {
	c, client, cancel := amqpOwnedFixture(t)
	f, err := amqpwire.ReadFrame(client, 512)
	if err != nil || len(f.Body) != 0 || f.Channel != 0 {
		t.Fatal("heartbeat", f, err)
	}
	reading := make(chan error, 1)
	go func() { _, err := c.read(); reading <- err }()
	cancel()
	c.close()
	c.close()
	select {
	case err := <-reading:
		if err == nil {
			t.Fatal("cancelled read succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("read not joined")
	}
	select {
	case <-c.heartbeatDone:
	default:
		t.Fatal("heartbeat not joined")
	}
}

func TestAMQPConnectionCancellationUnblocksHeartbeatWrite(t *testing.T) {
	c, _, cancel := amqpOwnedFixture(t)
	// The socket pair has no transport buffer. With its peer not reading, the
	// first heartbeat blocks in Write and cancellation must release it.
	deadline := time.Now().Add(2500 * time.Millisecond)
	for c.writeMu.TryLock() {
		c.writeMu.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("heartbeat never entered its write critical section")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	done := make(chan struct{})
	go func() { c.close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("blocked heartbeat owner did not join")
	}
}

func TestAMQPConnectionSerializedWrites(t *testing.T) {
	c, client, _ := amqpOwnedFixture(t)
	done := make(chan error, 2)
	for _, kind := range []byte{0x17, 0x18} {
		go func() { done <- c.write(amqpwire.Performative{Kind: kind}) }()
	}
	seen := map[byte]bool{}
	for len(seen) < 2 {
		f, err := amqpwire.ReadFrame(client, 512)
		if err != nil {
			t.Fatal(err)
		}
		p, err := amqpwire.DecodePerformative(f)
		if err != nil {
			t.Fatal("interleaved frame", err)
		}
		if p.Kind == 0 {
			continue
		}
		if seen[p.Kind] || p.Kind != 0x17 && p.Kind != 0x18 {
			t.Fatal("wrong serialized frame", p.Kind)
		}
		seen[p.Kind] = true
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}
