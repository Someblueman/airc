//go:build darwin || linux

package adapter

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestRedisStreamAdmissionDurableReplayAcrossReopenAndConsumerChanges(t *testing.T) {
	f := newAMQPAdmissionFixture(t)
	request := amqpRequest()
	broker := redisStreamBroker(f)
	request = redisStreamRequest(t, broker, request, "1-0")
	entry := redisStreamEntry(t, f, request, 0)
	route := broker.Route
	slots := make(chan struct{}, 1)
	var upstream string
	for i := range 3 {
		if i == 2 {
			broker.ID, broker.Group, broker.Consumer, broker.Address = "restarted", "another-group", "another-consumer", "127.0.0.1:6380"
		}
		a := admitRedisStream(t.Context(), f.d, broker, route.Stream, entry, slots)
		if a.refused || a.failure != nil || a.requestID != request.RequestID || a.binding != f.identity.Binding() || a.result == nil || a.result.Result == nil || !a.result.Result.Durable {
			t.Fatalf("durable admission: %+v", a)
		}
		job, err := f.s.Lookup(t.Context(), f.identity, request.RequestID)
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
			f.d, err = dispatch.New(f.s, f.v, f.reader, f.cursors, route.Operations)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	request.Message = "changed intent"
	a := admitRedisStream(t.Context(), f.d, broker, route.Stream, redisStreamEntry(t, f, request, 0), slots)
	if a.refused || a.result != nil || a.failure == nil || a.failure.Code != core.IDConflict || a.failure.Transient {
		t.Fatalf("conflict: %+v", a)
	}
	job, err := f.s.Lookup(t.Context(), f.identity, request.RequestID)
	if err != nil || job.UpstreamID != upstream || job.Request.Message == request.Message {
		t.Fatal("conflict changed job", err)
	}
}

func TestRedisStreamAdmissionDurableRevocationRejectsNewAndReplay(t *testing.T) {
	for _, replay := range []bool{false, true} {
		f := newAMQPAdmissionFixture(t)
		request := amqpRequest()
		broker := redisStreamBroker(f)
		request = redisStreamRequest(t, broker, request, "1-0")
		entry := redisStreamEntry(t, f, request, 0)
		route := broker.Route
		slots := make(chan struct{}, 1)
		if replay {
			if a := admitRedisStream(t.Context(), f.d, broker, route.Stream, entry, slots); a.result == nil || a.failure != nil || a.refused {
				t.Fatalf("initial admission: %+v", a)
			}
		}
		if _, err := f.s.Revoke(t.Context(), f.identity.ID()); err != nil {
			t.Fatal(err)
		}
		a := admitRedisStream(t.Context(), f.d, broker, route.Stream, entry, slots)
		if a.refused || a.result != nil || a.failure == nil || a.failure.Code != core.Forbidden || a.failure.Transient || a.requestID != request.RequestID || a.binding != f.identity.Binding() || len(slots) != 0 {
			t.Fatalf("revoked admission: %+v", a)
		}
	}
}
