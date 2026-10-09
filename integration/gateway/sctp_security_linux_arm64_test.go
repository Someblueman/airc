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

func TestSCTPRejectsSignedOnlyExpiredRealmMismatchAndRevokedIngress(t *testing.T) {
	f := newSCTPFixture(t, "ipv4")
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
			return adapter.FrameRequest{Version: 1, CorrelationID: "sctp-sctp-expired", Profile: "encrypted", Envelope: f.credentials.validityToken(t, r, now-300, now-120, true)}
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
	conn := dialSCTP(t, f.address)
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
	conn := dialSCTP(t, f.address)
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

func TestSCTPOversizedRecordClosesAssociationWithoutDispatch(t *testing.T) {
	f := newSCTPFixture(t, "ipv4")
	if err := f.client.sendRawRecord(t, make([]byte, sctpTestMessageLimit+1), false, time.Now().Add(5*time.Second)); err != nil {
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
	f.client = dialSCTP(t, f.address)
	response := sctpCall(t, f, f.client, core.Request{Version: 1, RequestID: "sctp-after-oversize", Operation: core.CapabilitiesGet})
	if response.Result == nil || response.Result.Capabilities == nil {
		t.Fatalf("fresh association did not resume after oversized record: %+v", response)
	}
	rpcNoJobs(t, f.config.StateDir)
}
