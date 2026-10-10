package adapter

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/amqpwire"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

func amqpPlainResult(t *testing.T, wire []byte) AMQPResult {
	t.Helper()
	message, err := amqpwire.DecodeMessage(wire)
	if err != nil {
		t.Fatal(err)
	}
	var response AMQPResult
	if err := json.Unmarshal(message.Body, &response); err != nil {
		t.Fatalf("result body is not plain JSON: %v %q", err, message.Body)
	}
	return response
}

func TestAMQPPlainResultNamesLinkPairAndInnerID(t *testing.T) {
	route := config.AMQPRoute{RequestAddress: "requests/a:send", ResponseAddress: "responses/a:read"}
	result := dispatch.DetachedResult{Version: 1, Operation: core.Send, Result: &core.Result{Version: 1, RequestID: "stable-inner-id", State: core.Queued, Durable: true}}
	wire, err := amqpResultMessage(route, "stable-inner-id", &result, nil)
	if err != nil {
		t.Fatal(err)
	}
	response := amqpPlainResult(t, wire)
	if response.Version != 1 || response.Protocol != "amqp/1" || response.RequestID != "stable-inner-id" || response.RequestAddress != route.RequestAddress || response.ResponseAddress != route.ResponseAddress || response.Result == nil || !response.Result.Result.Durable || response.Error != nil {
		t.Fatal(response)
	}
	wire, err = amqpResultMessage(route, "failed-id", nil, &core.Failure{Code: core.IDConflict})
	if err != nil {
		t.Fatal(err)
	}
	response = amqpPlainResult(t, wire)
	if response.RequestID != "failed-id" || response.Error == nil || response.Error.Code != core.IDConflict || response.Result != nil {
		t.Fatal(response)
	}
}

func TestAMQPPlainResultCapacityAndShape(t *testing.T) {
	route := config.AMQPRoute{RequestAddress: "request", ResponseAddress: "response"}
	result := dispatch.DetachedResult{Version: 1, Operation: core.Send, Result: &core.Result{Version: 1, RequestID: strings.Repeat("x", amqpwire.MaxMessageBody)}}
	if _, err := amqpResultMessage(route, "id", &result, nil); !errors.Is(err, errResultCapacity) {
		t.Fatal("oversized result", err)
	}
	valid := dispatch.DetachedResult{Version: 1, Operation: core.Send, Result: &core.Result{Version: 1, RequestID: "id"}}
	if _, err := amqpResultMessage(route, "", &valid, nil); !errors.Is(err, amqpwire.ErrValue) {
		t.Fatal("empty request ID", err)
	}
	if _, err := amqpResultMessage(route, "id", nil, nil); !errors.Is(err, amqpwire.ErrValue) {
		t.Fatal(err)
	}
	if _, err := amqpResultMessage(route, "id", &valid, &core.Failure{}); !errors.Is(err, amqpwire.ErrValue) {
		t.Fatal(err)
	}
}
