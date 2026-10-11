//go:build darwin || linux

package adapter

import (
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/amqpwire"
)

func TestAMQPListenerChannelRefusalClosesAndJoins(t *testing.T) {
	slots := make(chan struct{}, 1)
	_, dial, token, cancel, done := amqpListenerFixture(t, slots)
	client := dial()
	if err := client.HandshakeContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	amqpClientNegotiate(t, client, token)
	f, err := amqpwire.EncodePerformative(amqpwire.Performative{Kind: 0x17}, 1, 512)
	if err != nil {
		t.Fatal(err)
	}
	if err := amqpwire.WriteFrame(client, f, 512); err != nil {
		t.Fatal(err)
	}
	p := amqpClientRead(t, client)
	if p.Kind != 0x18 || string(p.Fields[0].Children[0].Children[0].Data) != "amqp:connection:framing-error" {
		t.Fatal(p)
	}
	amqpClientWrite(t, client, amqpwire.Performative{Kind: 0x18})
	waitAMQPSlots(t, slots, 0)
	cancel()
	if err := amqpListenerFinished(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestAMQPConnectionHeartbeatWriteFailureCancelsOwner(t *testing.T) {
	c, client, _ := amqpOwnedFixture(t)
	closeMQTTConnection(client)
	select {
	case <-c.heartbeatDone:
	case <-time.After(2500 * time.Millisecond):
		t.Fatal("heartbeat write failure did not stop writer")
	}
	if c.ctx.Err() == nil {
		t.Fatal("write failure left owner active")
	}
	c.close()
}
