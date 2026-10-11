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
	coapUDP "github.com/plgd-dev/go-coap/v3/udp"
	coapClient "github.com/plgd-dev/go-coap/v3/udp/client"
)

// coapUDPPost sends one plain frame as an ordinary CoAP POST and returns the
// response code and body (go-coap fetches Block2 continuations).
func coapUDPPost(t *testing.T, client *coapClient.Conn, frame frameRequest) (codes.Code, []byte) {
	t.Helper()
	body, err := json.Marshal(frame)
	if err != nil || len(body) > adapter.MaxRequestFrameBytes {
		t.Fatal("CoAP request size", len(body), err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	response, err := client.Post(ctx, "/v1/coap/rpc", message.AppJSON, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer client.ReleaseMessage(response)
	payload, err := response.ReadBody()
	if err != nil {
		t.Fatal(err)
	}
	return response.Code(), payload
}

func coapUDPCall(t *testing.T, f udpFixture, client *coapClient.Conn, request core.Request) adapter.FrameResponse {
	t.Helper()
	correlation := "coap-" + request.RequestID
	code, payload := coapUDPPost(t, client, frameRequest{Version: 1, CorrelationID: correlation, Profile: "plain", Request: f.credentials.token(t, request, true)})
	if code != codes.Content {
		t.Fatal("CoAP response code", code, string(payload))
	}
	response := decodeFrameResponse(t, payload)
	if response.Version != 1 || response.CorrelationID != correlation || response.Event != "result" || response.Sequence != 1 || response.Error != nil || response.Result == nil || response.Result.Operation != request.Operation {
		t.Fatal("CoAP correlated response", response)
	}
	return response
}

func coapUDPClient(t *testing.T, f udpFixture) *coapClient.Conn {
	t.Helper()
	client, err := coapUDP.Dial(f.address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func coapUDPAccepted(t *testing.T, f udpFixture, client *coapClient.Conn, request core.Request) core.Result {
	t.Helper()
	initial := coapUDPCall(t, f, client, request)
	if initial.Result.Result == nil || !initial.Result.Result.Durable || initial.Result.Result.RequestID != request.RequestID {
		t.Fatal("CoAP durable admission", initial)
	}
	deadline := time.Now().Add(5 * time.Second)
	for attempt := 0; time.Now().Before(deadline); attempt++ {
		receipt := coapUDPCall(t, f, client, core.Request{Version: 1, RequestID: fmt.Sprintf("%s-receipt-%d", request.RequestID, attempt), Operation: core.ReceiptGet, LookupID: request.RequestID})
		if receipt.Result.Result == nil || receipt.Result.Result.RequestID != request.RequestID {
			t.Fatal("CoAP receipt", receipt)
		}
		if receipt.Result.Result.State == core.Accepted {
			return *receipt.Result.Result
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatal("CoAP send did not reach accepted receipt")
	return core.Result{}
}
