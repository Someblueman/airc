//go:build linux && arm64

package gateway_test

import (
	"errors"
	"os"
	"testing"
	"time"
)

func TestSCTPShutdownJoinsAssociationBlockedInRead(t *testing.T) {
	f := newSCTPFixtureShutdown(t, "ipv4", false)
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
