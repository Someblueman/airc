//go:build darwin || linux

package adapter

import (
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
)

// TestRedisStreamAdmissionAutoIDPlainRequest models `XADD events * intent <json>`:
// the broker chooses the entry ID and the body is the HTTP contract request.
func TestRedisStreamAdmissionAutoIDPlainRequest(t *testing.T) {
	f := newRedisFixture(t)
	broker := redisStreamBroker()
	entry := rediswire.StreamEntry{ID: "1760140800123-7", Payload: []byte(`{"version":1,"request_id":"r1","operation":"send","target":{"kind":"room","name":"#fixture"},"message":"hello"}`)}
	slots := make(chan struct{}, 1)
	a := admitRedisStream(t.Context(), f.d, broker, broker.Route.Stream, entry, slots)
	if a.refused || a.failure != nil || a.requestID != "r1" || a.result == nil || a.result.Result == nil || !a.result.Result.Durable || a.result.Result.RequestID != "r1" || len(slots) != 0 {
		t.Fatalf("auto-ID admission: %+v", a)
	}
	job, err := f.s.Lookup(t.Context(), f.identity, "r1")
	if err != nil || job.Principal != broker.Route.Principal || job.Binding != f.c.Principals[0].Binding || job.Request.Message != "hello" || job.Request.Target == nil || *job.Request.Target != broker.Route.Target || job.Request.Operation != core.Send {
		t.Fatalf("journal attribution: %+v %v", job, err)
	}
	wire, err := redisStreamResultMessage(broker, entry.ID, a.requestID, a.result, a.failure)
	if err != nil {
		t.Fatal(err)
	}
	result := streamResult(t, broker, entry.ID, wire)
	if result.RequestID != "r1" || result.Error != nil || result.Result == nil || !result.Result.Result.Durable {
		t.Fatalf("auto-ID result: %s", wire)
	}
}

func TestRedisStreamAdmissionConfiguredRevocationRefusesCorrelation(t *testing.T) {
	f := newRedisFixture(t)
	c := f.c
	// Configuration refuses a Redis route to a revoked principal, so the route
	// is removed here to model a principal revoked while entries are pending.
	c.RedisPubSub = nil
	c.Principals = append([]config.Principal(nil), c.Principals...)
	c.Principals[0].Revoked = true
	c.Principals[0].BearerTokenFile = filepath.Join(filepath.Dir(c.StateDir), "bearer")
	if err := auth.CreateCredential(c.Principals[0].BearerTokenFile); err != nil {
		t.Fatal(err)
	}
	v, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	broker := redisStreamBroker()
	route := broker.Route
	d, err := dispatch.New(f.s, v, f.reader, f.cursors, route.Operations)
	if err != nil {
		t.Fatal(err)
	}
	slots := make(chan struct{}, 1)
	a := admitRedisStream(t.Context(), d, broker, route.Stream, redisStreamEntry(t, redisRequest()), slots)
	if !a.refused || a.result != nil || a.failure != nil || a.requestID != "" || len(slots) != 0 {
		t.Fatalf("revoked principal disclosed correlation: %+v", a)
	}
}
