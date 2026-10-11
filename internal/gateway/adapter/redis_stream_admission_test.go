//go:build darwin || linux

package adapter

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// The same request re-added under fresh auto-generated entry IDs, or redelivered
// to another group or consumer, is deduplicated by its request_id.
func TestRedisStreamAdmissionDurableReplayAcrossReopenAndConsumerChanges(t *testing.T) {
	f := newRedisFixture(t)
	request := redisRequest()
	broker := redisStreamBroker()
	entry := redisStreamEntry(t, request)
	route := broker.Route
	slots := make(chan struct{}, 1)
	var upstream string
	for i := range 3 {
		if i == 2 {
			broker.ID, broker.Group, broker.Consumer, broker.Address = "restarted", "another-group", "another-consumer", "127.0.0.1:6380"
		}
		entry.ID = []string{"1712345678901-0", "1712345678901-1", "1712345679999-0"}[i]
		a := admitRedisStream(t.Context(), f.d, f.author, broker, route.Stream, entry, slots)
		if a.refused || a.failure != nil || a.requestID != request.RequestID || a.result == nil || a.result.Result == nil || !a.result.Result.Durable {
			t.Fatalf("durable admission: %+v", a)
		}
		job, err := f.lookup(t, request.RequestID)
		if err != nil || job.Request.Message != request.Message || job.Request.Target == nil || *job.Request.Target != *request.Target || !job.Result.Durable || job.Result.SendAttempts != 0 || upstream != "" && upstream != job.UpstreamID {
			t.Fatal("journal intent changed", err)
		}
		upstream = job.UpstreamID
		if len(slots) != 0 {
			t.Fatal("request slot leaked")
		}
		if i == 0 {
			if err := f.s.Close(); err != nil {
				t.Fatal(err)
			}
			f.s, err = store.Open(t.Context(), f.c)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = f.s.Close() })
			f.d, err = dispatch.ForRoute(f.s, f.reader, f.cursors, "redis", redisTestOperations)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	request.Message = "changed intent"
	a := admitRedisStream(t.Context(), f.d, f.author, broker, route.Stream, redisStreamEntry(t, request), slots)
	if a.refused || a.result != nil || a.failure == nil || a.failure.Code != core.IDConflict || a.failure.Transient {
		t.Fatalf("conflict: %+v", a)
	}
	job, err := f.lookup(t, request.RequestID)
	if err != nil || job.UpstreamID != upstream || job.Request.Message == request.Message {
		t.Fatal("conflict changed job", err)
	}
}
