package adapter

import (
	"encoding/json"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
)

func redisStreamResultBroker() config.RedisStreams {
	r := redisResultRoute()
	return config.RedisStreams{Namespace: "broker", Route: config.RedisStreamRoute{Stream: "events", ResultStream: "results", Target: r.Target}}
}

func TestRedisStreamResultIsPlainJSONWithEntryCorrelation(t *testing.T) {
	b := redisStreamResultBroker()
	r := dispatch.DetachedResult{Version: 1, Operation: core.Send, Result: &core.Result{Version: 1, RequestID: "r1", Durable: true, State: core.Queued}}
	wire, err := redisStreamResultMessage(b, "1760140800123-7", "r1", &r, nil)
	if err != nil {
		t.Fatal(err)
	}
	var client map[string]any
	if err := json.Unmarshal(wire, &client); err != nil || client["request_id"] != "r1" || client["entry_id"] != "1760140800123-7" || client["protocol"] != "redis/streams" {
		t.Fatalf("client view %s: %v", wire, err)
	}
	var got RedisStreamResult
	if err := json.Unmarshal(wire, &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 || got.Namespace != b.Namespace || got.Stream != b.Route.Stream || got.ResultStream != b.Route.ResultStream || got.Target != b.Route.Target || got.Error != nil || got.Result == nil || !got.Result.Result.Durable || got.Result.Result.Accepted != nil {
		t.Fatal("correlation or durable acceptance changed")
	}
}

func TestRedisStreamResultLiteralNamesAndEntryIDs(t *testing.T) {
	b := redisStreamResultBroker()
	b.Route.Stream, b.Route.ResultStream = "literal.* events 世界", "literal results é"
	wire, err := redisStreamResultMessage(b, "1-0", "id", nil, &core.Failure{Code: core.IDConflict})
	if err != nil {
		t.Fatal(err)
	}
	var got RedisStreamResult
	if err := json.Unmarshal(wire, &got); err != nil || got.RequestID != "id" || got.EntryID != "1-0" || got.Stream != b.Route.Stream || got.ResultStream != b.Route.ResultStream || got.Result != nil || got.Error == nil || got.Error.Code != core.IDConflict {
		t.Fatal("correlated literal failure changed", err)
	}
	for _, entry := range []string{"", "0-0", "01-0", "1", ">"} {
		if wire, err := redisStreamResultMessage(b, entry, "id", nil, &core.Failure{}); err != rediswire.ErrFrame || wire != nil {
			t.Fatalf("malformed entry %q published", entry)
		}
	}
	for _, bad := range []string{"", "nul\x00", "line\n", "\xff", b.Route.Stream} {
		other := b
		other.Route.ResultStream = bad
		if wire, err := redisStreamResultMessage(other, "1-0", "id", nil, &core.Failure{}); err != rediswire.ErrFrame || wire != nil {
			t.Fatalf("invalid result stream %q published", bad)
		}
	}
}
