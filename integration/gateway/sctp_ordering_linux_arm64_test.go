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

func assertSCTPInvalidIngressCloses(t *testing.T, f sctpFixture, frame frameRequest) {
	t.Helper()
	conn := dialSCTP(t, f.address, f.mode)
	sctpSendFrame(t, conn, frame)
	wire, err := conn.readRecord(t, time.Now().Add(2*time.Second))
	if err == nil {
		t.Fatalf("unauthenticated SCTP ingress returned a response token: %q", wire)
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("invalid SCTP ingress neither closed the association nor returned a response: %s", frame.CorrelationID)
	}
	if closeErr := conn.close(); closeErr != nil && !errors.Is(closeErr, os.ErrClosed) {
		t.Errorf("close invalid SCTP association: %v", closeErr)
	}
}

func assertSCTPNoNativeIntake(t *testing.T, f sctpFixture) {
	t.Helper()
	rpcNoJobs(t, f.config.StateDir)
	if messages := nativeWebTransportMessages(t, f.config.Upstream.Address, "#gateway-fixture"); len(messages) != 0 {
		t.Fatalf("invalid SCTP ingress reached native AIRC: %+v", messages)
	}
}

func TestSCTPMismatchedRequestOrderingClosesBeforeIntake(t *testing.T) {
	for _, tc := range []struct {
		name         string
		listenerMode adapter.SCTPMode
		peerMode     adapter.SCTPMode
		sendMode     adapter.SCTPMode
	}{
		{"ordered-listener-rejects-unordered-peer", adapter.SCTPOrdered, adapter.SCTPUnordered, adapter.SCTPUnordered},
		{"unordered-listener-rejects-ordered-peer", adapter.SCTPUnordered, adapter.SCTPOrdered, adapter.SCTPOrdered},
		{"pr-ttl-listener-rejects-unordered-record", adapter.SCTPOrderedTTL, adapter.SCTPOrderedTTL, adapter.SCTPUnordered},
	} {
		name := tc.name
		t.Run(name, func(t *testing.T) {
			f := newSCTPFixtureMode(t, "ipv4", tc.listenerMode, true)
			conn := dialSCTP(t, f.address, tc.peerMode)
			request := core.Request{Version: 1, RequestID: "sctp-mismatched-ordering", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "wrong ordering flag"}
			frame := sctpFrame(t, f, request, true)
			if err := conn.sendRawRecord(t, sctpRequestBytes(t, frame), tc.sendMode, time.Now().Add(5*time.Second)); err != nil {
				t.Fatalf("send mismatched-order SCTP request: %v", err)
			}
			if response, err := conn.readRecord(t, time.Now().Add(2*time.Second)); err == nil {
				t.Fatalf("mismatched SCTP ordering returned a response: %q", response)
			} else if errors.Is(err, os.ErrDeadlineExceeded) {
				t.Fatal("mismatched SCTP ordering did not close the association")
			}
			if err := conn.close(); err != nil {
				t.Fatal(err)
			}
			assertSCTPNoNativeIntake(t, f)
		})
	}
}
