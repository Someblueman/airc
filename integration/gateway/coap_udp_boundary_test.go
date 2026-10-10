//go:build darwin || linux

package gateway_test

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestCoAPUDPUnverifiedInputHasNoMailboxOrNativeIntake(t *testing.T) {
	f := newPacketFixture(t, "coap_udp", nil)
	client := coapUDPClient(t, f)
	request := core.Request{Version: 1, RequestID: "coap-invalid", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "must never reach native"}
	for _, frame := range []adapter.FrameRequest{
		{Version: 1, CorrelationID: "signed-only", Profile: "signed", Envelope: f.credentials.token(t, request, false)},
		{Version: 1, CorrelationID: "tampered", Profile: "encrypted", Envelope: "x" + f.credentials.token(t, request, true)[1:]},
	} {
		coapUDPWriteFrame(t, client, frame)
		assertUDPSilent(t, f.mailbox)
	}
	if records := nativeWebTransportMessages(t, f.config.Bindings[0].Address, request.Target.Name); len(records) != 0 {
		t.Fatal("unverified CoAP posted", records)
	}
	rpcNoJobs(t, f.config.StateDir)
	// The same real client and mailbox remain usable after the rejected input.
	request.RequestID = "coap-valid-control"
	accepted := coapUDPAccepted(t, f, client, request)
	records := nativeWebTransportMessages(t, f.config.Bindings[0].Address, request.Target.Name)
	if len(records) != 1 || records[0].ID != accepted.MessageID || records[0].From != "fixture-author" || records[0].Target != request.Target.Name || records[0].Message != request.Message {
		t.Fatal("CoAP recovery control", records, accepted)
	}
}
