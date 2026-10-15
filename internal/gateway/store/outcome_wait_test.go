//go:build darwin || linux

package store

import (
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// TestAdmitAuthorAwait answers with the first outcome saved while the
// admission waits, answers queued when the wait ends first without leaving a
// waiter behind, and does not wait for a job that already has its outcome.
func TestAdmitAuthorAwait(t *testing.T) {
	c, _, _ := fixture(t)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, now := t.Context(), time.Now()
	sub := guest("alice", "unix")

	// An attempt completes while the admission waits.
	done := make(chan error, 1)
	go func() {
		<-s.Admitted()
		g, err := s.Reserve(ctx, "g:alice", "request", SendAttempt, now)
		if err == nil {
			_, err = g.Consume(now)
		}
		if err == nil {
			_, err = s.Complete(ctx, g, Completion{Failure: core.Failure{Code: core.UpstreamUnavailable, Transient: true}}, now)
		}
		done <- err
	}()
	j, err := s.AdmitAuthorAwait(ctx, sub, request(), 10*time.Second, now)
	if err != nil || j.Result.State != core.RetryWait || j.Result.SendAttempts != 1 {
		t.Fatal("admission did not answer with the saved outcome", j.Result, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	// Nothing delivers: the wait ends with the queued job.
	r := request()
	r.RequestID = "unanswered"
	start := time.Now()
	j, err = s.AdmitAuthorAwait(ctx, sub, r, 50*time.Millisecond, now)
	if err != nil || j.Result.State != core.Queued || time.Since(start) < 50*time.Millisecond {
		t.Fatal("timed-out admission", j.Result, err)
	}
	s.waitMu.Lock()
	left := len(s.waiters)
	s.waitMu.Unlock()
	if left != 0 {
		t.Fatal("a timed-out admission left its waiter", left)
	}

	// A replay of a job with an outcome answers at once.
	start = time.Now()
	j, err = s.AdmitAuthorAwait(ctx, sub, request(), 10*time.Second, now)
	if err != nil || j.Result.State != core.RetryWait || time.Since(start) > 5*time.Second {
		t.Fatal("replay waited", j.Result, err)
	}
}
