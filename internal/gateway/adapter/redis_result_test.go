package adapter

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

func redisResultFixture(t *testing.T) (*auth.ResponseSealer, ed25519.PublicKey, config.RedisRoute, auth.ResponseScope) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := auth.NewResponseSealer("response", key)
	if err != nil {
		t.Fatal(err)
	}
	route := config.RedisRoute{Principal: "principal", RequestChannel: "airc.request", ResponseChannel: "airc.response", Target: core.Target{Kind: "room", Name: "#fixture"}}
	return sealer, pub, route, redisResponseScope("realm", "principal", "binding", route)
}

func TestRedisSignedResultScopeAndCorrelation(t *testing.T) {
	sealer, pub, route, scope := redisResultFixture(t)
	now := time.Now()
	result := dispatch.DetachedResult{Version: 1, Operation: core.Send, Result: &core.Result{Version: 1, RequestID: "stable-id", State: core.Queued, Durable: true}}
	wire, err := redisResultMessage(sealer, scope, route, "stable-id", &result, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := auth.OpenSignedResponse(string(wire), "response", pub, scope, now)
	if err != nil {
		t.Fatal(err)
	}
	var response RedisResult
	if err := json.Unmarshal(plain, &response); err != nil {
		t.Fatal(err)
	}
	if response.Version != 1 || response.Protocol != "redis/pubsub" || response.RequestID != "stable-id" || response.RequestChannel != route.RequestChannel || response.ResponseChannel != route.ResponseChannel || response.Target != route.Target || response.Error != nil || response.Result == nil || !response.Result.Result.Durable || response.Result.Result.Accepted != nil {
		t.Fatal("correlation or acceptance changed")
	}
	for _, change := range []func(*config.RedisRoute){
		func(r *config.RedisRoute) { r.RequestChannel = "other.request" }, func(r *config.RedisRoute) { r.ResponseChannel = "other.response" }, func(r *config.RedisRoute) { r.RequestChannel, r.ResponseChannel = r.ResponseChannel, r.RequestChannel }, func(r *config.RedisRoute) { r.Target.Name = "#other" }, func(r *config.RedisRoute) { r.Target.Kind = "agent" },
	} {
		other := route
		change(&other)
		expected := redisResponseScope(scope.Realm, scope.Principal, scope.Binding, other)
		if _, err := auth.OpenSignedResponse(string(wire), "response", pub, expected, now); !errors.Is(err, auth.ErrAuthentication) {
			t.Fatal("cross-route response accepted", err)
		}
	}
	for _, other := range []auth.ResponseScope{
		natsResponseScope(scope.Realm, scope.Principal, scope.Binding, config.NATSRoute{RequestSubject: route.RequestChannel, ResponseSubject: route.ResponseChannel, Target: route.Target}),
		{Realm: "other", Principal: scope.Principal, Binding: scope.Binding, Route: scope.Route}, {Realm: scope.Realm, Principal: "other", Binding: scope.Binding, Route: scope.Route}, {Realm: scope.Realm, Principal: scope.Principal, Binding: "other", Route: scope.Route},
	} {
		if _, err := auth.OpenSignedResponse(string(wire), "response", pub, other, now); !errors.Is(err, auth.ErrAuthentication) {
			t.Fatal("cross-protocol/identity response accepted", err)
		}
	}
	if _, err := auth.OpenSignedResponse(string(wire), "response", pub, scope, now.Add(auth.ResponseLifetime+time.Duration(auth.ClockSkewSeconds+1)*time.Second)); !errors.Is(err, auth.ErrAuthentication) {
		t.Fatal("expired response", err)
	}
	wire, err = redisResultMessage(sealer, scope, route, "failed-id", nil, &core.Failure{Code: core.IDConflict}, now)
	if err != nil {
		t.Fatal(err)
	}
	plain, err = auth.OpenSignedResponse(string(wire), "response", pub, scope, now)
	if err != nil {
		t.Fatal(err)
	}
	response = RedisResult{}
	if err := json.Unmarshal(plain, &response); err != nil {
		t.Fatal(err)
	}
	if response.RequestID != "failed-id" || response.Error == nil || response.Error.Code != core.IDConflict || response.Result != nil {
		t.Fatal("failure correlation")
	}
}
