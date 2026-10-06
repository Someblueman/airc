//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func TestPlainCoAPTCPRejectsUnverifiedInputAndObserveBeforeNativeIntake(t *testing.T) {
	f := newCoAPTCPProfileFixture(t, true)
	request := core.Request{Version: 1, RequestID: "plain-coap-invalid", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "must never post"}
	watch := core.Request{Version: 1, RequestID: "plain-coap-stream-invalid", Operation: core.Watch, Target: request.Target, Start: "retained", Limit: 1}
	for _, test := range []struct {
		path  string
		frame adapter.FrameRequest
		code  codes.Code
	}{
		{"/v1/coap/rpc", adapter.FrameRequest{Version: 1, CorrelationID: "signed", Profile: "signed", Envelope: f.credentials.token(t, request, false)}, codes.BadRequest},
		{"/v1/coap/rpc", adapter.FrameRequest{Version: 1, CorrelationID: "tampered", Profile: "encrypted", Envelope: "x" + f.credentials.token(t, request, true)[1:]}, codes.Unauthorized},
		{"/v1/coap/rpc", adapter.FrameRequest{Version: 1, CorrelationID: "stream", Profile: "encrypted", Envelope: f.credentials.token(t, watch, true), Stream: true}, codes.BadRequest},
		{"/v1/coap/observe/register", adapter.FrameRequest{Version: 1, CorrelationID: "observe", Profile: "encrypted", Envelope: f.credentials.token(t, request, true), Stream: true}, codes.NotFound},
	} {
		body, err := json.Marshal(test.frame)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		response, err := f.conn.Post(ctx, test.path, message.AppJSON, bytes.NewReader(body))
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		payload, readErr := response.ReadBody()
		code := response.Code()
		f.conn.ReleaseMessage(response)
		if readErr != nil || code != test.code || len(payload) != 0 {
			t.Fatal("unverified request leaked content", code, payload, readErr)
		}
	}
	if records := nativeWebTransportMessages(t, f.config.Bindings[0].Address, request.Target.Name); len(records) != 0 {
		t.Fatal("unverified input posted", records)
	}
	rpcNoJobs(t, f.config.StateDir)
	request.RequestID = "plain-coap-valid"
	accepted := plainCoAPTCPAccepted(t, f, request)
	records := nativeWebTransportMessages(t, f.config.Bindings[0].Address, request.Target.Name)
	if len(records) != 1 || records[0].ID != accepted.MessageID || records[0].From != "fixture-author" || records[0].Target != request.Target.Name || records[0].Message != request.Message {
		t.Fatal("same-connection recovery", records, accepted)
	}
}
