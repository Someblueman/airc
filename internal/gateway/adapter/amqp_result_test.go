package adapter

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/amqpwire"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

func TestAMQPSignedResultBindsExactLinkPairAndInnerID(t *testing.T) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := auth.NewResponseSealer("response", key)
	if err != nil {
		t.Fatal(err)
	}
	route := config.AMQPRoute{RequestAddress: "requests/a:send", ResponseAddress: "responses/a:read"}
	scope := amqpResponseScope("realm", "principal", "binding", route)
	now := time.Now()
	result := dispatch.DetachedResult{Version: 1, Operation: core.Send, Result: &core.Result{Version: 1, RequestID: "stable-inner-id", State: core.Queued, Durable: true}}
	wire, err := amqpResultMessage(sealer, scope, route, "stable-inner-id", &result, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	message, err := amqpwire.DecodeMessage(wire)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := auth.OpenSignedResponse(string(message.Body), "response", pub, scope, now)
	if err != nil {
		t.Fatal(err)
	}
	var response AMQPResult
	if err := json.Unmarshal(plain, &response); err != nil {
		t.Fatal(err)
	}
	if response.Version != 1 || response.Protocol != "amqp/1" || response.RequestID != "stable-inner-id" || response.RequestAddress != route.RequestAddress || response.ResponseAddress != route.ResponseAddress || response.Result == nil || !response.Result.Result.Durable || response.Error != nil {
		t.Fatal(response)
	}
	for _, other := range []config.AMQPRoute{{RequestAddress: "requests/b:send", ResponseAddress: route.ResponseAddress}, {RequestAddress: route.RequestAddress, ResponseAddress: "responses/b:read"}, {RequestAddress: route.ResponseAddress, ResponseAddress: route.RequestAddress}} {
		expected := amqpResponseScope("realm", "principal", "binding", other)
		if _, err := auth.OpenSignedResponse(string(message.Body), "response", pub, expected, now); !errors.Is(err, auth.ErrAuthentication) {
			t.Fatal(other, err)
		}
	}
	wire, err = amqpResultMessage(sealer, scope, route, "failed-id", nil, &core.Failure{Code: core.IDConflict}, now)
	if err != nil {
		t.Fatal(err)
	}
	message, err = amqpwire.DecodeMessage(wire)
	if err != nil {
		t.Fatal(err)
	}
	plain, err = auth.OpenSignedResponse(string(message.Body), "response", pub, scope, now)
	if err != nil {
		t.Fatal(err)
	}
	response = AMQPResult{}
	if err := json.Unmarshal(plain, &response); err != nil {
		t.Fatal(err)
	}
	if response.RequestID != "failed-id" || response.Error == nil || response.Error.Code != core.IDConflict || response.Result != nil {
		t.Fatal(response)
	}
}
func TestAMQPSignedResultCapacityAndShape(t *testing.T) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := auth.NewResponseSealer("response", key)
	if err != nil {
		t.Fatal(err)
	}
	route := config.AMQPRoute{RequestAddress: "request", ResponseAddress: "response"}
	scope := amqpResponseScope("realm", "principal", "binding", route)
	result := dispatch.DetachedResult{Version: 1, Operation: core.Send, Result: &core.Result{Version: 1, RequestID: strings.Repeat("x", 25000)}}
	if _, err := amqpResultMessage(sealer, scope, route, "id", &result, nil, time.Now()); !errors.Is(err, auth.ErrResponseCapacity) {
		t.Fatal("oversized signed token", err)
	}
	if _, err := amqpResultMessage(sealer, scope, route, "id", nil, nil, time.Now()); !errors.Is(err, amqpwire.ErrValue) {
		t.Fatal(err)
	}
	if _, err := amqpResultMessage(sealer, scope, route, "id", &result, &core.Failure{}, time.Now()); !errors.Is(err, amqpwire.ErrValue) {
		t.Fatal(err)
	}
}
