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
	runSCTPRejectsSignedOnlyExpiredRealmMismatchAndRevokedIngress(t, false)
}

func TestSCTPUnorderedRejectsSignedOnlyExpiredRealmMismatchAndRevokedIngress(t *testing.T) {
	runSCTPRejectsSignedOnlyExpiredRealmMismatchAndRevokedIngress(t, true)
}

func runSCTPRejectsSignedOnlyExpiredRealmMismatchAndRevokedIngress(t *testing.T, unordered bool) {
	f := newSCTPFixtureMode(t, "ipv4", unordered, true)
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
	conn := dialSCTP(t, f.address, f.unordered)
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
	conn := dialSCTP(t, f.address, f.unordered)
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
	for _, listenerUnordered := range []bool{false, true} {
		name := "ordered-listener-rejects-unordered-peer"
		if listenerUnordered {
			name = "unordered-listener-rejects-ordered-peer"
		}
		t.Run(name, func(t *testing.T) {
			f := newSCTPFixtureMode(t, "ipv4", listenerUnordered, true)
			conn := dialSCTP(t, f.address, !listenerUnordered)
			request := core.Request{Version: 1, RequestID: "sctp-mismatched-ordering", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "wrong ordering flag"}
			sctpSendFrame(t, conn, sctpFrame(t, f, request, true))
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

func TestSCTPOversizedRecordClosesAssociationWithoutDispatch(t *testing.T) {
	runSCTPOversizedRecordClosesAssociationWithoutDispatch(t, false)
}

func TestSCTPUnorderedOversizedRecordClosesAssociationWithoutDispatch(t *testing.T) {
	runSCTPOversizedRecordClosesAssociationWithoutDispatch(t, true)
}

func runSCTPOversizedRecordClosesAssociationWithoutDispatch(t *testing.T, unordered bool) {
	f := newSCTPFixtureMode(t, "ipv4", unordered, true)
	if err := f.client.sendRawRecord(t, make([]byte, sctpTestMessageLimit+1), f.unordered, time.Now().Add(5*time.Second)); err != nil {
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

	// The oversized record is fatal to its association, so no residual tail can
	// be reinterpreted as the next request on a later connection.
	f.client = dialSCTP(t, f.address, f.unordered)
	response := sctpCall(t, f, f.client, core.Request{Version: 1, RequestID: "sctp-after-oversize", Operation: core.CapabilitiesGet})
	if response.Result == nil || response.Result.Capabilities == nil {
		t.Fatalf("fresh association did not resume after oversized record: %+v", response)
	}
	rpcNoJobs(t, f.config.StateDir)
}
