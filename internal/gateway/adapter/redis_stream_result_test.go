package adapter

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

func redisStreamResultFixture(t *testing.T) (*auth.ResponseSealer, ed25519.PublicKey, config.RedisStreams, auth.ResponseScope, string) {
	t.Helper()
	s, pub, r, _ := redisResultFixture(t)
	b := config.RedisStreams{Namespace: "broker", Route: config.RedisStreamRoute{Principal: r.Principal, Stream: "events", ResultStream: "results", Target: r.Target}}
	id, err := config.RedisStreamRequestID(b.Namespace, b.Route.Stream, "1-0")
	if err != nil {
		t.Fatal(err)
	}
	return s, pub, b, redisStreamResponseScope("realm", r.Principal, "binding", b, "1-0"), id
}

func TestRedisStreamSignedResultScope(t *testing.T) {
	s, pub, b, scope, id := redisStreamResultFixture(t)
	now := time.Now()
	r := dispatch.DetachedResult{Version: 1, Operation: core.Send, Result: &core.Result{Version: 1, RequestID: id, Durable: true, State: core.Queued}}
	wire, err := redisStreamResultMessage(s, scope, b, "1-0", id, &r, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := auth.OpenSignedResponse(string(wire), "response", pub, scope, now)
	if err != nil {
		t.Fatal(err)
	}
	var got RedisStreamResult
	if err := json.Unmarshal(plain, &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 || got.Protocol != "redis/streams" || got.RequestID != id || got.Namespace != b.Namespace || got.Stream != b.Route.Stream || got.ResultStream != b.Route.ResultStream || got.EntryID != "1-0" || got.Target != b.Route.Target || got.Error != nil || got.Result == nil || !got.Result.Result.Durable || got.Result.Result.Accepted != nil {
		t.Fatal("scope or durable acceptance changed")
	}
	for _, mutate := range []func(*config.RedisStreams){
		func(b *config.RedisStreams) { b.Namespace = "other" },
		func(b *config.RedisStreams) { b.Route.Stream = "other" },
		func(b *config.RedisStreams) { b.Route.ResultStream = "other" },
		func(b *config.RedisStreams) {
			b.Route.Stream, b.Route.ResultStream = b.Route.ResultStream, b.Route.Stream
		},
		func(b *config.RedisStreams) { b.Route.Target.Name = "#other" },
		func(b *config.RedisStreams) { b.Route.Target.Kind = "agent" },
	} {
		other := b
		mutate(&other)
		expected := redisStreamResponseScope(scope.Realm, scope.Principal, scope.Binding, other, "1-0")
		if _, err := auth.OpenSignedResponse(string(wire), "response", pub, expected, now); !errors.Is(err, auth.ErrAuthentication) {
			t.Fatal("foreign route accepted", err)
		}
	}
	for _, expected := range []auth.ResponseScope{
		redisStreamResponseScope(scope.Realm, scope.Principal, scope.Binding, b, "1-1"),
		redisResponseScope(scope.Realm, scope.Principal, scope.Binding, config.RedisRoute{RequestChannel: b.Route.Stream, ResponseChannel: b.Route.ResultStream, Target: b.Route.Target}),
		{Realm: "other", Principal: scope.Principal, Binding: scope.Binding, Route: scope.Route},
		{Realm: scope.Realm, Principal: "other", Binding: scope.Binding, Route: scope.Route},
		{Realm: scope.Realm, Principal: scope.Principal, Binding: "other", Route: scope.Route},
	} {
		if _, err := auth.OpenSignedResponse(string(wire), "response", pub, expected, now); !errors.Is(err, auth.ErrAuthentication) {
			t.Fatal("foreign identity/protocol accepted", err)
		}
	}
	if _, err := auth.OpenSignedResponse(string(wire), "response", pub, scope, now.Add(auth.ResponseLifetime+time.Duration(auth.ClockSkewSeconds+1)*time.Second)); !errors.Is(err, auth.ErrAuthentication) {
		t.Fatal("expired response accepted", err)
	}
}
