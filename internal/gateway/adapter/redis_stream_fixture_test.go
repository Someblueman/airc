//go:build darwin || linux

package adapter

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
)

func redisStreamBroker() config.RedisStreams {
	r := redisAdmissionRoute()
	return config.RedisStreams{ID: "streams", Namespace: "broker", Address: "127.0.0.1:6379", Group: "gateway", Consumer: "owner",
		Route: config.RedisStreamRoute{Principal: r.Principal, Stream: "events", ResultStream: "results", Target: r.Target, Operations: r.Operations}}
}

// redisStreamEntry carries a plain contract request. The entry ID is broker
// bookkeeping only; the request's own request_id is its journal identity.
func redisStreamEntry(t *testing.T, r core.Request) rediswire.StreamEntry {
	t.Helper()
	return rediswire.StreamEntry{ID: "1-0", Payload: redisPlainRequest(t, r)}
}
