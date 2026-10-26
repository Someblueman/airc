package backend

import (
	"testing"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
)

// Jobs send AIRC the client's request ID unchanged. Anything else is refused
// before a session opens.
func TestUpstreamIdentityIsTheRequestID(t *testing.T) {
	b := &Backend{realm: "realm"}
	job := store.Job{Scope: "g:alice", Request: core.Request{RequestID: "r-1"}}
	for id, want := range map[string]bool{"r-1": true, "r-2": false, "": false} {
		job.UpstreamID = id
		if got := b.upstreamIdentity(job); got != want {
			t.Errorf("upstream ID %q accepted=%v, want %v", id, got, want)
		}
	}
	job.Scope, job.UpstreamID = "alice", "r-1"
	if b.upstreamIdentity(job) {
		t.Error("invalid author scope accepted")
	}
}
