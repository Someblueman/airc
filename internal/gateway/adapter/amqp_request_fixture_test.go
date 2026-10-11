//go:build darwin || linux

package adapter

import (
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/amqpwire"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func amqpRequest() core.Request {
	return core.Request{Version: 1, RequestID: "amqp-stable-request", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#fixture"}, Message: "exact body\n世界"}
}

// amqpIncomingBody wraps a plain contract request body, exactly as an HTTP
// client would send it, in one complete AMQP data-section delivery.
func amqpIncomingBody(t *testing.T, body []byte) *amqpIncoming {
	t.Helper()
	wire, err := amqpwire.EncodeDataMessage(body)
	if err != nil {
		t.Fatal(err)
	}
	return &amqpIncoming{id: 42, tag: []byte("wire"), encoded: wire, complete: true, deadline: time.Now().Add(amqpMessageDeadline)}
}
