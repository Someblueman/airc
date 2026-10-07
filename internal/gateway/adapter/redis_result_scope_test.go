package adapter

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
)

func TestRedisResponseScopeContractAndLiteralChannels(t *testing.T) {
	sealer, pub, route, scope := redisResultFixture(t)
	// Fixed protocol vector lets independent clients reproduce the route digest.
	if scope.Route != "e9f8f2683ac7db3ef268c575a6b18dbf81a6ead265e62a8d28015411c9d2dad3" {
		t.Fatal("response scope contract changed")
	}
	route.RequestChannel, route.ResponseChannel = "literal.* request 世界", "literal response é"
	scope = redisResponseScope(scope.Realm, scope.Principal, scope.Binding, route)
	now := time.Now()
	wire, err := redisResultMessage(sealer, scope, route, "request", nil, &core.Failure{Code: core.CapacityExceeded, Transient: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := auth.OpenSignedResponse(string(wire), "response", pub, scope, now)
	if err != nil {
		t.Fatal(err)
	}
	var got RedisResult
	if err := json.Unmarshal(plain, &got); err != nil {
		t.Fatal(err)
	}
	if got.RequestChannel != route.RequestChannel || got.ResponseChannel != route.ResponseChannel {
		t.Fatal("literal channel bytes changed")
	}
	for _, bad := range []string{"", "nul\x00channel", "line\nchannel", "\xff"} {
		other := route
		other.RequestChannel = bad
		expected := redisResponseScope(scope.Realm, scope.Principal, scope.Binding, other)
		if wire, err := redisResultMessage(sealer, expected, other, "request", nil, &core.Failure{}, now); err != rediswire.ErrFrame || wire != nil {
			t.Fatal("invalid literal channel signed")
		}
	}
}
