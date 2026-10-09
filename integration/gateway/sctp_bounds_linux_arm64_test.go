//go:build linux && arm64

package gateway_test

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestSCTPOversizedRecordClosesAssociationWithoutDispatch(t *testing.T) {
	runSCTPOversizedRecordClosesAssociationWithoutDispatch(t, adapter.SCTPOrdered)
}

func TestSCTPUnorderedOversizedRecordClosesAssociationWithoutDispatch(t *testing.T) {
	runSCTPOversizedRecordClosesAssociationWithoutDispatch(t, adapter.SCTPUnordered)
}

func TestSCTPPRTTLOversizedRecordClosesAssociationWithoutDispatch(t *testing.T) {
	runSCTPOversizedRecordClosesAssociationWithoutDispatch(t, adapter.SCTPOrderedTTL)
}

func runSCTPOversizedRecordClosesAssociationWithoutDispatch(t *testing.T, mode adapter.SCTPMode) {
	f := newSCTPFixtureMode(t, "ipv4", mode, true)
	if err := f.client.sendRawRecord(t, make([]byte, sctpTestMessageLimit+1), f.mode, time.Now().Add(5*time.Second)); err != nil {
		t.Fatalf("send oversized native SCTP record: %v", err)
	}
	if response, err := f.client.readRecord(t, time.Now().Add(2*time.Second)); err == nil {
		t.Fatalf("oversized SCTP record produced a response: %q", response)
	} else if errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatal("oversized SCTP record did not close its association")
	}
	if err := f.client.close(); err != nil && !errors.Is(err, os.ErrClosed) {
		t.Fatal(err)
	}
	assertSCTPNoNativeIntake(t, f)

	// An oversized record is fatal to its association, so its residual tail
	// cannot be reinterpreted as the next request on a later connection.
	f.client = dialSCTP(t, f.address, f.mode)
	response := sctpCall(t, f, f.client, core.Request{Version: 1, RequestID: "sctp-after-oversize", Operation: core.CapabilitiesGet})
	if response.Result == nil || response.Result.Capabilities == nil {
		t.Fatalf("fresh association did not resume after oversized record: %+v", response)
	}
	rpcNoJobs(t, f.config.StateDir)
}
