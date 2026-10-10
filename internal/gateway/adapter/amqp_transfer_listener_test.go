//go:build darwin || linux

package adapter

import (
	"github.com/Someblueman/airc/internal/gateway/amqpwire"
	"testing"
)

func TestAMQPListenerRefusesTransferWithoutRequestCredit(t *testing.T) {
	slots := make(chan struct{}, 1)
	_, dial, token, cancel, done := amqpListenerFixture(t, slots, 1)
	client := dial()
	if err := client.HandshakeContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	amqpClientNegotiate(t, client, token)
	amqpClientWrite(t, client, amqpAttach(true, 1, "request"))
	if p := amqpClientRead(t, client); p.Kind != 0x12 {
		t.Fatal(p)
	}
	amqpClientWrite(t, client, amqpTransfer(1, []byte("uncredited request"), false))
	p := amqpClientRead(t, client)
	if p.Kind != 0x16 || p.Fields[0].Number != 0 || p.Fields[1].Number != 1 || string(p.Fields[2].Children[0].Children[0].Data) != "amqp:link:transfer-limit-exceeded" {
		t.Fatal(p)
	}
	if p := amqpClientRead(t, client); p.Kind != 0x18 {
		t.Fatal(p)
	}
	amqpClientWrite(t, client, amqpwire.Performative{Kind: 0x18})
	waitAMQPSlots(t, slots, 0)
	cancel()
	if err := amqpListenerFinished(t, done); err != nil {
		t.Fatal(err)
	}
}
