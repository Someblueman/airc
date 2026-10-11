//go:build darwin || linux

package adapter

import (
	"errors"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// TestOSCORERPCFullAdmissionBeforeIntake: a full request slot answers 5.03
// with a transient failure and keeps the slot; TestFrameCarriers covers
// intake, replay and frame refusals.
func TestOSCORERPCFullAdmissionBeforeIntake(t *testing.T) {
	f, body, _ := oscoreRPCFixture(t)
	requests := make(chan struct{}, 1)
	requests <- struct{}{}
	code, response := oscoreRPCRequest(t.Context(), body, f.d, requests, f.ingress())
	if code != codes.ServiceUnavailable || response.Error == nil || response.Error.Code != core.CapacityExceeded || !response.Error.Transient || len(requests) != 1 {
		t.Fatal("full admission accepted", code, response)
	}
	if _, err := f.lookup(t, "oscore-rpc-message"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("rejected request reached intake", err)
	}
}
