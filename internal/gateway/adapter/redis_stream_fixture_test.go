//go:build darwin || linux

package adapter

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
)

func redisStreamBroker(f amqpAdmissionFixture) config.RedisStreams {
	r := f.route()
	return config.RedisStreams{ID: "streams", Namespace: "broker", Address: "127.0.0.1:6379", Group: "gateway", Consumer: "owner",
		Route: config.RedisStreamRoute{Principal: r.Principal, Stream: "events", ResultStream: "results", Target: r.Target, Operations: r.Operations}}
}

func redisStreamRequest(t *testing.T, b config.RedisStreams, r core.Request, entry string) core.Request {
	t.Helper()
	id, err := config.RedisStreamRequestID(b.Namespace, b.Route.Stream, entry)
	if err != nil {
		t.Fatal(err)
	}
	r.RequestID = id
	return r
}

func redisStreamEntry(t *testing.T, f amqpAdmissionFixture, r core.Request, shift int64) rediswire.StreamEntry {
	t.Helper()
	return rediswire.StreamEntry{ID: "1-0", Payload: redisSignedRequest(t, f, r, shift)}
}
