//go:build darwin || linux

package adapter

import (
	"github.com/Someblueman/airc/internal/gateway/amqpwire"
	"testing"
)

func TestAMQPListenerFlowEchoAndUnknownHandle(t *testing.T) {
	slots := make(chan struct{}, 1)
	_, dial, token, cancel, done := amqpListenerFixture(t, slots)
	client := dial()
	if err := client.HandshakeContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	amqpClientNegotiate(t, client, token)
	amqpClientWrite(t, client, amqpAttach(false, 0, "response"))
	if p := amqpClientRead(t, client); p.Kind != 0x12 {
		t.Fatal(p)
	}
	p := amqpwire.Performative{Kind: 0x13, Fields: []amqpwire.Value{{Code: 0x70, Number: 1}, {Code: 0x70, Number: 8}, {Code: 0x70, Number: 7}, {Code: 0x70, Number: 8}, {Code: 0x70}, {Code: 0x70}, {Code: 0x70, Number: 3}, {Code: 0x40}, {Code: 0x56}, {Code: 0x56, Number: 1}}}
	amqpClientWrite(t, client, p)
	if r := amqpClientRead(t, client); r.Kind != 0x13 || r.Fields[6].Number != 3 {
		t.Fatal(r)
	}
	p.Fields[4].Number = 1
	amqpClientWrite(t, client, p)
	if r := amqpClientRead(t, client); r.Kind != 0x17 || string(r.Fields[0].Children[0].Children[0].Data) != "amqp:session:unattached-handle" {
		t.Fatal(r)
	}
	if r := amqpClientRead(t, client); r.Kind != 0x18 {
		t.Fatal(r)
	}
	amqpClientWrite(t, client, amqpwire.Performative{Kind: 0x18})
	waitAMQPSlots(t, slots, 0)
	cancel()
	if err := amqpListenerFinished(t, done); err != nil {
		t.Fatal(err)
	}
}
