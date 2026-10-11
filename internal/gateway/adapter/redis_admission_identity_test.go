//go:build darwin || linux

package adapter

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// TestRedisAdmissionPlainRequestPostsAsItsAuthor sends exactly the
// contract-v1 body an HTTP client would, with no credential or signature.
func TestRedisAdmissionPlainRequestPostsAsItsAuthor(t *testing.T) {
	f := newRedisFixture(t)
	route := redisAdmissionRoute()
	payload := []byte(`{"version":1,"request_id":"r1","operation":"send","target":{"kind":"room","name":"#fixture"},"message":"hello 世界","author":{"nick":"redis-guest"}}`)
	slots := make(chan struct{}, 1)
	a := admitRedis(t.Context(), f.d, f.author, "route:redis", route, route.RequestChannel, payload, slots)
	if a.refused || a.failure != nil || a.requestID != "r1" || a.result == nil || a.result.Result == nil || !a.result.Result.Durable || len(slots) != 0 {
		t.Fatalf("plain admission: %+v", a)
	}
	job, err := f.lookup(t, "r1")
	if err != nil || job.Author != (core.Author{Nick: redisTestAuthor}) || job.Request.Message != "hello 世界" || job.Request.Target == nil || *job.Request.Target != route.Target || job.Request.Operation != core.Send {
		t.Fatalf("journal attribution: %+v %v", job, err)
	}
}

// TestRedisAdmissionBoundAuthorPolicy: a request without an author posts as
// the route's bound author; a bound route refuses guest body authors with a
// correlated failure, and a guest route refuses anonymous requests.
func TestRedisAdmissionBoundAuthorPolicy(t *testing.T) {
	f := newRedisFixture(t)
	route := redisAdmissionRoute()
	bound, err := dispatch.NewRouteAuthor(nil, true)
	if err != nil {
		t.Fatal(err)
	}
	slots := make(chan struct{}, 1)
	anonymous := []byte(`{"version":1,"request_id":"r2","operation":"send","target":{"kind":"room","name":"#fixture"},"message":"hello"}`)
	if a := admitRedis(t.Context(), f.d, bound, "route:redis", route, route.RequestChannel, anonymous, slots); a.refused || a.failure == nil || a.failure.Code != core.InvalidRequest || a.requestID != "r2" {
		t.Fatalf("anonymous request on a guest route: %+v", a)
	}
	rf := &routeFixture{root: privateTestDir(t)}
	bound = rf.author(t, "alerts", true, false)
	if a := admitRedis(t.Context(), f.d, bound, "route:redis", route, route.RequestChannel, anonymous, slots); a.refused || a.failure != nil || a.result == nil {
		t.Fatalf("bound author: %+v", a)
	}
	credential, _ := core.NewCredential(authorTestToken)
	job, err := f.s.LookupAuthor(t.Context(), store.Submission{Author: core.Author{Nick: "alerts", Registered: true}, Credential: credential}, "r2")
	if err != nil || job.Author != (core.Author{Nick: "alerts", Registered: true}) {
		t.Fatalf("bound job: %+v %v", job.Author, err)
	}
	guest := redisPlainRequest(t, core.Request{Version: 1, RequestID: "r3", Operation: core.Send, Target: &route.Target, Message: "hi"})
	if a := admitRedis(t.Context(), f.d, bound, "route:redis", route, route.RequestChannel, guest, slots); a.refused || a.failure == nil || a.failure.Code != core.Forbidden || a.requestID != "r3" || len(slots) != 0 {
		t.Fatalf("guest on a bound route: %+v", a)
	}
}
