//go:build darwin || linux

package adapter

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// TestRedisStreamAdmissionAutoIDPlainRequest models `XADD events * intent <json>`:
// the broker chooses the entry ID and the body is the HTTP contract request.
func TestRedisStreamAdmissionAutoIDPlainRequest(t *testing.T) {
	f := newRedisFixture(t)
	broker := redisStreamBroker()
	entry := rediswire.StreamEntry{ID: "1760140800123-7", Payload: []byte(`{"version":1,"request_id":"r1","operation":"send","target":{"kind":"room","name":"#fixture"},"message":"hello","author":{"nick":"redis-guest"}}`)}
	slots := make(chan struct{}, 1)
	a := admitRedisStream(t.Context(), f.d, f.author, broker, broker.Route.Stream, entry, slots)
	if a.refused || a.failure != nil || a.requestID != "r1" || a.result == nil || a.result.Result == nil || !a.result.Result.Durable || a.result.Result.RequestID != "r1" || len(slots) != 0 {
		t.Fatalf("auto-ID admission: %+v", a)
	}
	job, err := f.lookup(t, "r1")
	if err != nil || job.Author != (core.Author{Nick: redisTestAuthor}) || job.Request.Message != "hello" || job.Request.Target == nil || *job.Request.Target != broker.Route.Target || job.Request.Operation != core.Send {
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

func TestRedisStreamAdmissionBoundAuthor(t *testing.T) {
	f := newRedisFixture(t)
	broker := redisStreamBroker()
	rf := &routeFixture{root: privateTestDir(t)}
	bound := rf.author(t, "stream-bot", false, false)
	entry := rediswire.StreamEntry{ID: "1760140800123-8", Payload: []byte(`{"version":1,"request_id":"r2","operation":"send","target":{"kind":"room","name":"#fixture"},"message":"hello"}`)}
	slots := make(chan struct{}, 1)
	if a := admitRedisStream(t.Context(), f.d, bound, broker, broker.Route.Stream, entry, slots); a.refused || a.failure != nil || a.result == nil {
		t.Fatalf("bound author: %+v", a)
	}
	job, err := f.s.LookupAuthor(t.Context(), store.Submission{Author: core.Author{Nick: "stream-bot"}}, "r2")
	if err != nil || job.Author != (core.Author{Nick: "stream-bot"}) {
		t.Fatalf("bound job: %+v %v", job.Author, err)
	}
}
