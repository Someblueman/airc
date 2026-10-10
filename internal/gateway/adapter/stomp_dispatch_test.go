//go:build darwin || linux

package adapter

import (
	"bufio"
	"encoding/json"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/stompwire"
)

func stompReadFrame(t *testing.T, r *bufio.Reader, command string) stompwire.Frame {
	t.Helper()
	f, err := stompwire.Read(r)
	if err != nil || f.Command != command {
		t.Fatalf("want %s, got %+v: %v", command, f, err)
	}
	return f
}

func stompSubscribeFrame() stompwire.Frame {
	return stompwire.Frame{Command: "SUBSCRIBE", Headers: []stompwire.Header{{Key: "id", Value: "subscription"}, {Key: "destination", Value: "response"}, {Key: "ack", Value: "client-individual"}, {Key: "receipt", Value: "subscribed"}}}
}

func stompOpenResult(t *testing.T, message stompwire.Frame) STOMPResult {
	t.Helper()
	destination, _ := message.Header("destination")
	subscription, _ := message.Header("subscription")
	ack, _ := message.Header("ack")
	contentType, _ := message.Header("content-type")
	if destination != "response" || subscription != "subscription" || ack == "" || contentType != "application/json" {
		t.Fatal(message)
	}
	var result STOMPResult
	if err := json.Unmarshal(message.Body, &result); err != nil {
		t.Fatalf("result body is not plain JSON: %v %q", err, message.Body)
	}
	return result
}

func TestSTOMPDispatchDurableReceiptACKAndReconnectReplay(t *testing.T) {
	slots := make(chan struct{}, 2)
	_, dial, token, cancel, done, fixture := stompDispatchFixture(t, slots, 1)
	request := amqpRequest()
	request.RequestID = "stomp-dispatched"
	send := stompRequestFrame(t, request)
	var upstream, previousAck string
	for attempt := range 3 {
		client := dial()
		r, connected := stompClientConnect(t, client, token)
		if connected.Command != "CONNECTED" {
			t.Fatal(connected)
		}
		stompClientWrite(t, client, stompSubscribeFrame())
		stompReadFrame(t, r, "RECEIPT")
		stompClientWrite(t, client, send)
		receipt := stompReadFrame(t, r, "RECEIPT")
		if id, _ := receipt.Header("receipt-id"); id != "wire-receipt" {
			t.Fatal(receipt)
		}
		job, err := fixture.s.Lookup(t.Context(), fixture.identity, request.RequestID)
		if err != nil {
			t.Fatal("receipt before durable job", err)
		}
		if attempt == 0 {
			upstream = job.UpstreamID
		}
		if job.UpstreamID != upstream || job.Request.Message != request.Message || job.Result.SendAttempts != 0 {
			t.Fatal(job)
		}
		message := stompReadFrame(t, r, "MESSAGE")
		ackID, _ := message.Header("ack")
		if ackID == previousAck {
			t.Fatal("reconnect reused message/acknowledgement ID")
		}
		previousAck = ackID
		result := stompOpenResult(t, message)
		if result.RequestID != request.RequestID || result.Result == nil || result.Result.Result == nil || !result.Result.Result.Durable {
			t.Fatal(result)
		}
		if attempt == 1 {
			ack, _ := message.Header("ack")
			stompClientWrite(t, client, stompwire.Frame{Command: "ACK", Headers: []stompwire.Header{{Key: "id", Value: ack}, {Key: "receipt", Value: "acked"}}})
			stompReadFrame(t, r, "RECEIPT")
			stompClientWrite(t, client, send)
			stompReadFrame(t, r, "RECEIPT")
			stompReadFrame(t, r, "MESSAGE")
		}
		closeMQTTConnection(client)
		waitAMQPSlots(t, slots, 0)
	}
	cancel()
	amqpListenerFinished(t, done)
}
