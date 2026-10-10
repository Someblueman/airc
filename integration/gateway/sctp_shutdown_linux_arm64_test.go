//go:build linux && arm64

package gateway_test

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
)

func TestSCTPShutdownJoinsAssociationBlockedInRead(t *testing.T) {
	runSCTPShutdownJoinsAssociationBlockedInRead(t, adapter.SCTPOrdered)
}

func TestSCTPUnorderedShutdownJoinsAssociationBlockedInRead(t *testing.T) {
	runSCTPShutdownJoinsAssociationBlockedInRead(t, adapter.SCTPUnordered)
}

func TestSCTPPRTTLShutdownJoinsAssociationBlockedInRead(t *testing.T) {
	runSCTPShutdownJoinsAssociationBlockedInRead(t, adapter.SCTPOrderedTTL)
}

func runSCTPShutdownJoinsAssociationBlockedInRead(t *testing.T, mode adapter.SCTPMode) {
	f := newSCTPFixtureMode(t, "ipv4", mode, false)
	if err := f.process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("interrupt SCTP gateway: %v", err)
	}
	select {
	case <-f.process.done:
		if f.process.err != nil {
			t.Fatalf("SCTP gateway shutdown: %v: %s", f.process.err, f.process.stderr.String())
		}
	case <-time.After(8 * time.Second):
		t.Fatal("SCTP gateway did not join its idle association")
	}
	if _, err := f.client.readRecord(t, time.Now().Add(2*time.Second)); err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("gateway shutdown did not close blocked SCTP read: %v", err)
	}
	assertSCTPNoNativeIntake(t, f)
}
