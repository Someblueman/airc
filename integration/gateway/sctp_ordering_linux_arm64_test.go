//go:build linux && arm64

package gateway_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestSCTPRejectsSignedOnlyExpiredRealmMismatchAndRevokedIngress(t *testing.T) {
	runSCTPRejectsSignedOnlyExpiredRealmMismatchAndRevokedIngress(t, adapter.SCTPOrdered)
}

func TestSCTPUnorderedRejectsSignedOnlyExpiredRealmMismatchAndRevokedIngress(t *testing.T) {
	runSCTPRejectsSignedOnlyExpiredRealmMismatchAndRevokedIngress(t, adapter.SCTPUnordered)
}

func TestSCTPPRTTLRejectsSignedOnlyExpiredRealmMismatchAndRevokedIngress(t *testing.T) {
	runSCTPRejectsSignedOnlyExpiredRealmMismatchAndRevokedIngress(t, adapter.SCTPOrderedTTL)
}

func runSCTPRejectsSignedOnlyExpiredRealmMismatchAndRevokedIngress(t *testing.T, mode adapter.SCTPMode) {
	f := newSCTPFixtureMode(t, "ipv4", mode, true)
	baseRequest := func(id string) core.Request {
		return core.Request{Version: 1, RequestID: id, Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "must not post"}
	}
	now := time.Now().Unix()
	wrongRealm := *f.credentials
	wrongRealm.realm = "other-realm"
	cases := []struct {
		name  string
		frame func() adapter.FrameRequest
	}{
		{name: "signed-only", frame: func() adapter.FrameRequest { return sctpFrame(t, f, baseRequest("sctp-signed-only"), false) }},
		{name: "expired", frame: func() adapter.FrameRequest {
			r := baseRequest("sctp-expired")
			envelope := f.credentials.validityToken(t, r, now-300, now-120, true)
			digest := sha256.Sum256([]byte(envelope))
			return adapter.FrameRequest{Version: 1, CorrelationID: hex.EncodeToString(digest[:]), Profile: "encrypted", Envelope: envelope}
		}},
		{name: "realm-mismatch", frame: func() adapter.FrameRequest {
			return sctpFrame(t, sctpFixture{credentials: &wrongRealm}, baseRequest("sctp-wrong-realm"), true)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertSCTPInvalidIngressCloses(t, f, tc.frame())
		})
	}
	revokeWebRTCPrincipal(t, f.config.StateDir, f.config.Principals[0].ID)
	conn := dialSCTP(t, f.address, f.mode)
	frame := sctpFrame(t, f, baseRequest("sctp-revoked"), true)
	sctpSendFrame(t, conn, frame)
	response := sctpReadResponse(t, f, conn, frame.CorrelationID, time.Now().Add(2*time.Second))
	if response.Event != "error" || response.Sequence != 1 || response.Error == nil || response.Error.Code != core.Forbidden || response.Error.Transient {
		t.Fatalf("revoked SCTP ingress refusal=%+v", response)
	}
	if err := conn.close(); err != nil {
		t.Fatal(err)
	}
	assertSCTPNoNativeIntake(t, f)
}

func assertSCTPInvalidIngressCloses(t *testing.T, f sctpFixture, frame adapter.FrameRequest) {
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
	if messages := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture"); len(messages) != 0 {
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
