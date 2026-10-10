//go:build darwin || linux

package adapter

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/stompwire"
)

func TestSTOMPDispatchSubscriptionAndPendingRefusals(t *testing.T) {
	for _, name := range []string{"missing subscription", "foreign destination", "cumulative ack", "duplicate subscription", "unknown header", "wrong ack", "pending send", "pending unsubscribe", "nack"} {
		t.Run(name, func(t *testing.T) {
			slots := make(chan struct{}, 1)
			_, dial, token, cancel, done, fixture, _ := stompDispatchFixture(t, slots, 1)
			client := dial()
			r, connected := stompClientConnect(t, client, token)
			if connected.Command != "CONNECTED" {
				t.Fatal(connected)
			}
			sub := stompSubscribeFrame()
			send := stompAdmissionFrame(t, fixture, amqpRequest(), 0)
			pending := name == "wrong ack" || name == "pending send" || name == "pending unsubscribe" || name == "nack"
			if name == "duplicate subscription" || pending {
				stompClientWrite(t, client, sub)
				stompReadFrame(t, r, "RECEIPT")
			}
			var message stompwire.Frame
			if pending {
				stompClientWrite(t, client, send)
				stompReadFrame(t, r, "RECEIPT")
				message = stompReadFrame(t, r, "MESSAGE")
			}
			bad := sub
			switch name {
			case "missing subscription", "pending send":
				bad = send
			case "foreign destination":
				bad.Headers[1].Value = "foreign"
			case "cumulative ack":
				bad.Headers[2].Value = "client"
			case "unknown header":
				bad.Headers = append(bad.Headers, stompwire.Header{Key: "selector", Value: "anything"})
			case "wrong ack":
				bad = stompwire.Frame{Command: "ACK", Headers: []stompwire.Header{{Key: "id", Value: "other"}}}
			case "pending unsubscribe":
				bad = stompwire.Frame{Command: "UNSUBSCRIBE", Headers: []stompwire.Header{{Key: "id", Value: "subscription"}}}
			case "nack":
				ack, _ := message.Header("ack")
				bad = stompwire.Frame{Command: "NACK", Headers: []stompwire.Header{{Key: "id", Value: ack}}}
			}
			stompClientWrite(t, client, bad)
			stompReadFrame(t, r, "ERROR")
			if f, err := stompwire.Read(r); err == nil {
				t.Fatal("frame after terminal ERROR", f)
			}
			waitAMQPSlots(t, slots, 0)
			if !pending {
				if _, err := fixture.s.Lookup(t.Context(), fixture.identity, amqpRequest().RequestID); err == nil {
					t.Fatal("refused request admitted")
				}
			}
			cancel()
			amqpListenerFinished(t, done)
		})
	}
}
