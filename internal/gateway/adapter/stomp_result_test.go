package adapter

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/stompwire"
)

func TestSTOMPSignedResultBindsExactDestinationsAndInnerID(t *testing.T) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := auth.NewResponseSealer("response", key)
	if err != nil {
		t.Fatal(err)
	}
	route := config.STOMPRoute{RequestDestination: "requests/a:send", ResponseDestination: "responses/a:read"}
	scope := stompResponseScope("realm", "principal", "binding", route)
	now := time.Now()
	result := dispatch.DetachedResult{Version: 1, Operation: core.Send, Result: &core.Result{Version: 1, RequestID: "stable-inner-id", State: core.Queued, Durable: true}}
	wire, err := stompResultMessage(sealer, scope, route, "stable-inner-id", &result, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := auth.OpenSignedResponse(string(wire), "response", pub, scope, now)
	if err != nil {
		t.Fatal(err)
	}
	var response STOMPResult
	if err := json.Unmarshal(plain, &response); err != nil {
		t.Fatal(err)
	}
	if response.Version != 1 || response.Protocol != "stomp/1.2" || response.RequestID != "stable-inner-id" || response.RequestDestination != route.RequestDestination || response.ResponseDestination != route.ResponseDestination || response.Result == nil || !response.Result.Result.Durable || response.Error != nil {
		t.Fatal(response)
	}
	for _, expected := range []auth.ResponseScope{
		stompResponseScope("other", "principal", "binding", route),
		stompResponseScope("realm", "other", "binding", route),
		stompResponseScope("realm", "principal", "other", route),
		amqpResponseScope("realm", "principal", "binding", config.AMQPRoute{RequestAddress: route.RequestDestination, ResponseAddress: route.ResponseDestination}),
	} {
		if _, err := auth.OpenSignedResponse(string(wire), "response", pub, expected, now); !errors.Is(err, auth.ErrAuthentication) {
			t.Fatal("accepted foreign response scope", expected, err)
		}
	}
	if _, err := auth.OpenSignedResponse(string(wire), "response", pub, scope, now.Add(auth.ResponseLifetime+time.Duration(auth.ClockSkewSeconds+1)*time.Second)); !errors.Is(err, auth.ErrAuthentication) {
		t.Fatal("accepted expired result", err)
	}
	for _, other := range []config.STOMPRoute{{RequestDestination: "requests/b:send", ResponseDestination: route.ResponseDestination}, {RequestDestination: route.RequestDestination, ResponseDestination: "responses/b:read"}, {RequestDestination: route.ResponseDestination, ResponseDestination: route.RequestDestination}} {
		expected := stompResponseScope("realm", "principal", "binding", other)
		if _, err := auth.OpenSignedResponse(string(wire), "response", pub, expected, now); !errors.Is(err, auth.ErrAuthentication) {
			t.Fatal(other, err)
		}
	}
	wire, err = stompResultMessage(sealer, scope, route, "failed-id", nil, &core.Failure{Code: core.IDConflict}, now)
	if err != nil {
		t.Fatal(err)
	}
	plain, err = auth.OpenSignedResponse(string(wire), "response", pub, scope, now)
	if err != nil {
		t.Fatal(err)
	}
	response = STOMPResult{}
	if err := json.Unmarshal(plain, &response); err != nil {
		t.Fatal(err)
	}
	if response.RequestID != "failed-id" || response.Error == nil || response.Error.Code != core.IDConflict || response.Result != nil {
		t.Fatal(response)
	}
}
func TestSTOMPSignedResultCapacityAndShape(t *testing.T) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := auth.NewResponseSealer("response", key)
	if err != nil {
		t.Fatal(err)
	}
	route := config.STOMPRoute{RequestDestination: "request", ResponseDestination: "response"}
	scope := stompResponseScope("realm", "principal", "binding", route)
	result := dispatch.DetachedResult{Version: 1, Operation: core.Send, Result: &core.Result{Version: 1, RequestID: strings.Repeat("x", 25000)}}
	if _, err := stompResultMessage(sealer, scope, route, "id", &result, nil, time.Now()); !errors.Is(err, auth.ErrResponseCapacity) {
		t.Fatal("oversized signed token", err)
	}
	if _, err := stompResultMessage(sealer, scope, route, "", &result, nil, time.Now()); !errors.Is(err, stompwire.ErrFrame) {
		t.Fatal("empty request ID", err)
	}
	if _, err := stompResultMessage(sealer, scope, route, "id", nil, nil, time.Now()); !errors.Is(err, stompwire.ErrFrame) {
		t.Fatal(err)
	}
	if _, err := stompResultMessage(sealer, scope, route, "id", &result, &core.Failure{}, time.Now()); !errors.Is(err, stompwire.ErrFrame) {
		t.Fatal(err)
	}
}
