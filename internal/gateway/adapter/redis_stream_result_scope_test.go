package adapter

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
)

func TestRedisStreamResponseScopeContract(t *testing.T) {
	s, pub, b, scope, _ := redisStreamResultFixture(t)
	if scope.Route != "052adb17547817c9f58dc07c16f58d0d88abea1895f61fc882414a36ef8d6458" {
		t.Fatal("scope vector changed")
	}
	b.Route.Stream, b.Route.ResultStream = "literal.* events 世界", "literal results é"
	scope = redisStreamResponseScope(scope.Realm, scope.Principal, scope.Binding, b, "1-0")
	id, err := config.RedisStreamRequestID(b.Namespace, b.Route.Stream, "1-0")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	wire, err := redisStreamResultMessage(s, scope, b, "1-0", id, nil, &core.Failure{Code: core.IDConflict}, now)
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
	if got.RequestID != id || got.EntryID != "1-0" || got.Stream != b.Route.Stream || got.ResultStream != b.Route.ResultStream || got.Result != nil || got.Error == nil || got.Error.Code != core.IDConflict {
		t.Fatal("correlated literal failure changed")
	}
	for _, entry := range []string{"0-0", "01-0", "1-1"} {
		other := redisStreamResponseScope(scope.Realm, scope.Principal, scope.Binding, b, entry)
		if wire, err := redisStreamResultMessage(s, other, b, entry, id, nil, &core.Failure{}, now); err != rediswire.ErrFrame || wire != nil {
			t.Fatal("foreign or malformed entry signed")
		}
	}
	for _, bad := range []string{"", "nul\x00", "line\n", "\xff"} {
		other := b
		other.Route.ResultStream = bad
		expected := redisStreamResponseScope(scope.Realm, scope.Principal, scope.Binding, other, "1-0")
		if wire, err := redisStreamResultMessage(s, expected, other, "1-0", id, nil, &core.Failure{}, now); err != rediswire.ErrFrame || wire != nil {
			t.Fatal("invalid result stream signed")
		}
	}
	other := b
	other.Route.ResultStream = b.Route.Stream
	expected := redisStreamResponseScope(scope.Realm, scope.Principal, scope.Binding, other, "1-0")
	if _, err := redisStreamResultMessage(s, expected, other, "1-0", id, nil, &core.Failure{}, now); err != rediswire.ErrFrame {
		t.Fatal("stream roles overlap")
	}
}
