//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func plainCoAPTCPCall(t *testing.T, f coapTCPFixture, request core.Request) adapter.FrameResponse {
	t.Helper()
	correlation := "plain-coap-" + request.RequestID
	frame := frameRequest{Version: 1, CorrelationID: correlation, Profile: "plain", Request: f.credentials.token(t, request, true)}
	body, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	response, err := f.conn.Post(ctx, "/v1/coap/rpc", message.AppJSON, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer f.conn.ReleaseMessage(response)
	payload, err := response.ReadBody()
	if err != nil || response.Code() != codes.Content {
		t.Fatal("CoAP plain response", response.Code(), err)
	}
	result := decodeFrameResponse(t, payload)
	if result.Version != 1 || result.CorrelationID != correlation || result.Event != "result" || result.Sequence != 1 || result.Error != nil || result.Result == nil || result.Result.Operation != request.Operation {
		t.Fatal("CoAP plain result", result)
	}
	return result
}

func plainCoAPTCPAccepted(t *testing.T, f coapTCPFixture, request core.Request) core.Result {
	t.Helper()
	admission := plainCoAPTCPCall(t, f, request)
	if admission.Result.Result == nil || !admission.Result.Result.Durable || admission.Result.Result.RequestID != request.RequestID {
		t.Fatal("CoAP durable admission", admission)
	}
	deadline := time.Now().Add(5 * time.Second)
	for attempt := 0; time.Now().Before(deadline); attempt++ {
		receipt := plainCoAPTCPCall(t, f, core.Request{Version: 1, RequestID: fmt.Sprintf("%s-receipt-%d", request.RequestID, attempt), Operation: core.ReceiptGet, LookupID: request.RequestID})
		if receipt.Result.Result == nil || receipt.Result.Result.RequestID != request.RequestID {
			t.Fatal("CoAP receipt", receipt)
		}
		if receipt.Result.Result.State == core.Accepted {
			return *receipt.Result.Result
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatal("CoAP native receipt remained unaccepted")
	return core.Result{}
}
