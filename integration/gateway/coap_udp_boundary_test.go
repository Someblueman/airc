//go:build darwin || linux

package gateway_test

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func TestCoAPUDPMalformedInputHasNoNativeIntake(t *testing.T) {
	f := newPacketFixture(t, "coap_udp", nil)
	client := coapUDPClient(t, f)
	request := core.Request{Version: 1, RequestID: "coap-invalid", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "must never reach native"}
	for _, frame := range []frameRequest{
		{Version: 1, CorrelationID: "unknown-profile", Profile: "unknown", Request: f.credentials.token(t, request, false)},
		{Version: 1, CorrelationID: "malformed", Profile: "plain", Request: "[]"},
	} {
		if code, payload := coapUDPPost(t, client, frame); code != codes.BadRequest || len(payload) != 0 {
			t.Fatal("malformed CoAP request answered", frame.CorrelationID, code, string(payload))
		}
	}
	if records := nativeWebTransportMessages(t, f.config.Upstream.Address, request.Target.Name); len(records) != 0 {
		t.Fatal("malformed CoAP posted", records)
	}
	rpcNoJobs(t, f.config.StateDir)
	// The same real client remains usable after the rejected input.
	request.RequestID = "coap-valid-control"
	accepted := coapUDPAccepted(t, f, client, request)
	records := nativeWebTransportMessages(t, f.config.Upstream.Address, request.Target.Name)
	if len(records) != 1 || records[0].ID != accepted.MessageID || records[0].From != "fixture-author" || records[0].Target != request.Target.Name || records[0].Message != request.Message {
		t.Fatal("CoAP recovery control", records, accepted)
	}
}
