package backend

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// New jobs send AIRC the client's request ID unchanged; jobs journaled by an
// earlier gateway keep their v2 digest. Anything else is refused before a
// session opens.
func TestUpstreamIdentityAcceptsCurrentAndJournaledLegacyIDs(t *testing.T) {
	b := &Backend{realm: "realm"}
	job := store.Job{Scope: "g:alice", Request: core.Request{RequestID: "r-1"}}
	legacy, err := core.LegacyUpstreamID("realm", "g:alice", "r-1")
	if err != nil {
		t.Fatal(err)
	}
	otherRealm, _ := core.LegacyUpstreamID("other", "g:alice", "r-1")
	otherAuthor, _ := core.LegacyUpstreamID("realm", "g:bob", "r-1")
	for id, want := range map[string]bool{"r-1": true, legacy: true, "r-2": false, otherRealm: false, otherAuthor: false, "": false} {
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
