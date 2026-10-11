//go:build darwin || linux

package adapter

import (
	"github.com/Someblueman/airc/internal/gateway/amqpwire"
	"github.com/Someblueman/airc/internal/gateway/core"
	"testing"
)

// A send-only client (no response link) is granted credit and learns each
// request's outcome from its disposition alone.
func TestAMQPListenerSendOnlyCreditAndDisposition(t *testing.T) {
	slots := make(chan struct{}, 1)
	_, dial, token, cancel, done := amqpListenerFixture(t, slots)
	client := dial()
	if err := client.HandshakeContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	amqpClientNegotiate(t, client, token)
	amqpClientWrite(t, client, amqpAttach(true, 1, "request"))
	if p := amqpClientRead(t, client); p.Kind != 0x12 {
		t.Fatal(p)
	}
	if flow := amqpClientRead(t, client); flow.Kind != 0x13 || flow.Fields[6].Number != 1 {
		t.Fatal(flow)
	}
	for i, tc := range []struct {
		body    string
		outcome uint64
	}{
		{`{"version":1,"request_id":"send-only","operation":"send","target":{"kind":"room","name":"#fixture"},"message":"hi","author":{"nick":"amqp-guest"}}`, 0x24},
		{`{"version":1,"request_id":"send-only-2","operation":"send","target":{"kind":"room","name":"#other"},"message":"hi","author":{"nick":"amqp-guest"}}`, 0x25},
	} {
		wire, err := amqpwire.EncodeDataMessage([]byte(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		amqpClientWrite(t, client, amqpTransfer(uint32(i), wire, false))
		disposition := amqpClientRead(t, client)
		if disposition.Kind != 0x15 || disposition.Fields[1].Number != uint64(i) || disposition.Fields[4].Descriptor.Number != tc.outcome {
			t.Fatal(disposition)
		}
		if tc.outcome == 0x25 {
			// The failure code rides in the rejected error's description.
			e := disposition.Fields[4].Children[0].Children[0].Children[0].Children
			if len(e) < 2 || string(e[1].Data) != string(core.Forbidden) {
				t.Fatal("rejection without failure code", e)
			}
		}
		// The session window refresh, then one new request credit.
		if flow := amqpClientRead(t, client); flow.Kind != 0x13 || flow.Fields[6].Number != 0 {
			t.Fatal(flow)
		}
		if flow := amqpClientRead(t, client); flow.Kind != 0x13 || flow.Fields[6].Number != 1 {
			t.Fatal(flow)
		}
	}
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
