//go:build darwin || linux

package adapter

import (
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

// TestRedisAdmissionPlainRequestPostsAsRoutePrincipal sends exactly the
// contract-v1 body an HTTP client would, with no credential or signature.
func TestRedisAdmissionPlainRequestPostsAsRoutePrincipal(t *testing.T) {
	f := newRedisFixture(t)
	route := redisAdmissionRoute()
	payload := []byte(`{"version":1,"request_id":"r1","operation":"send","target":{"kind":"room","name":"#fixture"},"message":"hello 世界"}`)
	slots := make(chan struct{}, 1)
	a := admitRedis(t.Context(), f.d, route, route.RequestChannel, payload, slots)
	if a.refused || a.failure != nil || a.requestID != "r1" || a.result == nil || a.result.Result == nil || !a.result.Result.Durable || len(slots) != 0 {
		t.Fatalf("plain admission: %+v", a)
	}
	job, err := f.s.Lookup(t.Context(), f.identity, "r1")
	if err != nil || job.Principal != route.Principal || job.Binding != f.c.Principals[0].Binding || job.Request.Message != "hello 世界" || job.Request.Target == nil || *job.Request.Target != route.Target || job.Request.Operation != core.Send {
		t.Fatalf("journal attribution: %+v %v", job, err)
	}
}

func TestRedisAdmissionConfiguredRevocationRefusesCorrelation(t *testing.T) {
	f := newRedisFixture(t)
	c := f.c
	// Configuration refuses a Redis route to a revoked principal, so the route
	// is removed here to model a principal revoked while requests are in flight.
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
	route := redisAdmissionRoute()
	d, err := dispatch.New(f.s, v, f.reader, f.cursors, route.Operations)
	if err != nil {
		t.Fatal(err)
	}
	slots := make(chan struct{}, 1)
	a := admitRedis(t.Context(), d, route, route.RequestChannel, redisPlainRequest(t, redisRequest()), slots)
	if !a.refused || a.result != nil || a.failure != nil || a.requestID != "" || len(slots) != 0 {
		t.Fatalf("revoked principal disclosed correlation: %+v", a)
	}
}
