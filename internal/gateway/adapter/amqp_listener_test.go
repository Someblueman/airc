//go:build darwin || linux

package adapter

import (
	"errors"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/amqpwire"
)

func TestAMQPListenerSharedAdmissionAndJoinedCancellation(t *testing.T) {
	slots := make(chan struct{}, 2)
	slots <- struct{}{} // A different transport already owns one shared slot.
	_, dial, _, cancel, done := amqpListenerFixture(t, slots)
	first := dial()
	waitAMQPSlots(t, slots, 2)
	second := dial()
	if err := second.HandshakeContext(t.Context()); err == nil {
		t.Fatal("over-capacity TLS socket admitted")
	}
	if len(slots) != 2 {
		t.Fatal("refusal changed foreign capacity")
	}
	cancel()
	if err := amqpListenerFinished(t, done); err != nil {
		t.Fatal(err)
	}
	if len(slots) != 1 {
		t.Fatal("shutdown leaked or stole a shared slot")
	}
	var b [1]byte
	if _, err := first.Read(b[:]); err == nil {
		t.Fatal("incomplete TLS negotiation remains live")
	}
	<-slots
}

func TestAMQPListenerCloseAndAcceptFailure(t *testing.T) {
	for _, kind := range []string{"close", "read_failure", "accept_failure"} {
		t.Run(kind, func(t *testing.T) {
			slots := make(chan struct{}, 1)
			l, dial, token, cancel, done := amqpListenerFixture(t, slots)
			client := dial()
			if kind == "accept_failure" {
				waitAMQPSlots(t, slots, 1)
				_ = l.Close()
				if err := amqpListenerFinished(t, done); !errors.Is(err, net.ErrClosed) {
					t.Fatal(err)
				}
			} else {
				if err := client.HandshakeContext(t.Context()); err != nil {
					t.Fatal(err)
				}
				amqpClientNegotiate(t, client, token)
				if kind == "read_failure" {
					closeMQTTConnection(client)
				} else {
					amqpClientWrite(t, client, amqpwire.Performative{Kind: 0x18})
					if p := amqpClientRead(t, client); p.Kind != 0x18 {
						t.Fatal(p)
					}
				}
				waitAMQPSlots(t, slots, 0)
				cancel()
				if err := amqpListenerFinished(t, done); err != nil {
					t.Fatal(err)
				}
			}
			if len(slots) != 0 {
				t.Fatal("connection owner not joined")
			}
		})
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
