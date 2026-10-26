// Package testwait waits in tests for a condition rather than a fixed time.
package testwait

import (
	"testing"
	"time"
)

// Until polls condition until it holds, failing the test with what if it does
// not within the given time. It is a real synchronisation point: the deadline
// only bounds a failure, and a passing wait returns as soon as the condition
// holds.
func Until(t testing.TB, within time.Duration, what string, condition func() bool) {
	t.Helper()
	Every(t, within, 2*time.Millisecond, what, condition)
}

// Every is Until with a caller-chosen polling interval, for conditions that
// cost a round trip to evaluate.
func Every(t testing.TB, within, interval time.Duration, what string, condition func() bool) {
	t.Helper()
	if !Poll(within, interval, condition) {
		t.Fatalf("timed out after %s waiting for %s", within, what)
	}
}

// Poll reports whether condition holds within the given time, checking it
// every interval. It is for waits that cannot fail the test themselves: in a
// goroutine other than the test's own, where t.Fatal is not allowed, or in a
// helper that returns the failure to its caller. Every is Poll plus the
// failure.
func Poll(within, interval time.Duration, condition func() bool) bool {
	deadline := time.Now().Add(within)
	for !condition() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(interval)
	}
	return true
}
