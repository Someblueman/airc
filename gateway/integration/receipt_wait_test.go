package gateway_test

import (
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/internal/testwait"
)

// acceptedReceipt polls receipt, one carrier's receipt read for a request,
// until the result it returns is Accepted, within the bound at the given
// interval, and returns that result. Otherwise it fails the test naming what
// it waited for and the last receipt it saw. A nil receipt is one not yet
// readable.
func acceptedReceipt(t *testing.T, within, interval time.Duration, what string, receipt func() *core.Result) core.Result {
	t.Helper()
	var last *core.Result
	if !testwait.Poll(within, interval, func() bool {
		last = receipt()
		return last != nil && last.State == core.Accepted
	}) {
		if last == nil {
			t.Fatalf("timed out after %s waiting for %s; no receipt", within, what)
		}
		t.Fatalf("timed out after %s waiting for %s; last receipt %+v", within, what, *last)
	}
	return *last
}
