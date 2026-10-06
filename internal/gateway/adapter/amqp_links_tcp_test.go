//go:build darwin || linux

package adapter

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/amqpwire"
)

func TestAMQPListenerFixedLinkPairAndAuthorityRefusal(t *testing.T) {
	for _, unauthorized := range []bool{false, true} {
		slots := make(chan struct{}, 1)
		_, dial, token, cancel, done := amqpListenerFixture(t, slots, 1)
		client := dial()
		if err := client.HandshakeContext(t.Context()); err != nil {
			t.Fatal(err)
		}
		amqpClientNegotiate(t, client, token)
		request := "request"
		if unauthorized {
			request = "unknown"
		}
		amqpClientWrite(t, client, amqpAttach(true, 1, request))
		p := amqpClientRead(t, client)
		if unauthorized {
			if p.Kind != 0x18 || string(p.Fields[0].Children[0].Children[0].Data) != "amqp:unauthorized-access" {
				t.Fatal(p)
			}
		} else {
			if p.Kind != 0x12 || p.Fields[1].Number != 0 || p.Fields[2].Number != 1 || string(p.Fields[6].Children[0].Children[0].Data) != "request" {
				t.Fatal(p)
			}
			amqpClientWrite(t, client, amqpAttach(false, 0, "response"))
			p = amqpClientRead(t, client)
			if p.Kind != 0x12 || p.Fields[1].Number != 1 || p.Fields[2].Number != 0 || string(p.Fields[5].Children[0].Children[0].Data) != "response" {
				t.Fatal(p)
			}
		}
		amqpClientWrite(t, client, amqpwire.Performative{Kind: 0x18})
		if !unauthorized {
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
}
