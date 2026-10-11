//go:build darwin || linux

package adapter

import (
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/stompwire"
)

func TestSTOMPDispatchSubscriptionAndPendingRefusals(t *testing.T) {
	for _, name := range []string{"foreign destination", "cumulative ack", "duplicate subscription", "unknown header", "wrong ack", "pending send", "pending unsubscribe", "nack"} {
		t.Run(name, func(t *testing.T) {
			slots := make(chan struct{}, 1)
			_, dial, token, cancel, done, fixture := stompDispatchFixture(t, slots)
			client := dial()
			r, connected := stompClientConnect(t, client, token)
			if connected.Command != "CONNECTED" {
				t.Fatal(connected)
			}
			sub := stompSubscribeFrame()
			send := stompRequestFrame(t, amqpRequest())
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
			case "pending send":
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
				if _, err := fixture.lookup(t, amqpRequest().RequestID); err == nil {
					t.Fatal("refused request admitted")
				}
			}
			cancel()
			amqpListenerFinished(t, done)
		})
	}
}

// A SEND without a subscription is answered by its receipt only, after
// durable intake; a refused one by a terminal ERROR carrying the failure.
func TestSTOMPDispatchSendWithoutSubscription(t *testing.T) {
	slots := make(chan struct{}, 1)
	_, dial, token, cancel, done, fixture := stompDispatchFixture(t, slots)
	client := dial()
	r, connected := stompClientConnect(t, client, token)
	if connected.Command != "CONNECTED" {
		t.Fatal(connected)
	}
	stompClientWrite(t, client, stompRequestFrame(t, amqpRequest()))
	stompReadFrame(t, r, "RECEIPT")
	if _, err := fixture.lookup(t, amqpRequest().RequestID); err != nil {
		t.Fatal("receipt before intake", err)
	}
	other := amqpRequest()
	other.RequestID = "other-target"
	other.Target.Name = "#other"
	stompClientWrite(t, client, stompRequestFrame(t, other))
	failure := stompReadFrame(t, r, "ERROR")
	if !strings.Contains(string(failure.Body), `"forbidden"`) {
		t.Fatalf("ERROR without failure: %s", failure.Body)
	}
	waitAMQPSlots(t, slots, 0)
	cancel()
	if err := amqpListenerFinished(t, done); err != nil {
		t.Fatal(err)
	}
}
