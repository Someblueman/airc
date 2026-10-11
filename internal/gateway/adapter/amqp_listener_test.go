//go:build darwin || linux

package adapter

import (
	"errors"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/amqpwire"
)

// TestAMQPListenerCloseReleasesOwner: a peer Close is answered with Close
// and frees the slot. TestTLSListenerCarriers covers the shared slots.
func TestAMQPListenerCloseReleasesOwner(t *testing.T) {
	slots := make(chan struct{}, 1)
	_, dial, token, cancel, done := amqpListenerFixture(t, slots)
	client := dial()
	if err := client.HandshakeContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	amqpClientNegotiate(t, client, token)
	amqpClientWrite(t, client, amqpwire.Performative{Kind: 0x18})
	if p := amqpClientRead(t, client); p.Kind != 0x18 {
		t.Fatal(p)
	}
	waitAMQPSlots(t, slots, 0)
	cancel()
	if err := amqpListenerFinished(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestAMQPListenerNoFramesAfterClose(t *testing.T) {
	slots := make(chan struct{}, 2)
	_, dial, token, cancel, done := amqpListenerFixture(t, slots)
	first := dial()
	if err := first.HandshakeContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	amqpClientNegotiate(t, first, token)
	// A repeated Begin is refused with Close, and nothing follows the Close
	// while the server waits a bounded time for the peer's.
	amqpClientWrite(t, first, amqpwire.Performative{Kind: 0x11, Fields: []amqpwire.Value{{Code: 0x40}, {Code: 0x70}, {Code: 0x70, Number: 8}, {Code: 0x70, Number: 8}}})
	p := amqpClientRead(t, first)
	if p.Kind != 0x18 || string(p.Fields[0].Children[0].Children[0].Data) != "amqp:illegal-state" {
		t.Fatal(p)
	}
	if err := first.SetReadDeadline(time.Now().Add(1200 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := amqpwire.ReadFrame(first, 512); err == nil {
		t.Fatal("frame sent after Close")
	} else {
		var timeout net.Error
		if !errors.As(err, &timeout) || !timeout.Timeout() {
			t.Fatal("closing socket terminated before peer acknowledgement", err)
		}
	}
	if err := first.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	amqpClientWrite(t, first, amqpwire.Performative{Kind: 0x18})
	waitAMQPSlots(t, slots, 0)
	cancel()
	if err := amqpListenerFinished(t, done); err != nil {
		t.Fatal(err)
	}
}
