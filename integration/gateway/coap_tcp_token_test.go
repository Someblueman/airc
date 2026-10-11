//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func TestCoAPTCPRequestsAndObservationTokensCannotBorrowAuthority(t *testing.T) {
	f := newCoAPTCPFixture(t)
	address := f.config.Upstream.Address
	statusClient := nativeStatusClient(t, address)
	baseline := nativeObserverCount(t, statusClient)
	for _, profile := range []string{"signed", "encrypted"} {
		frame := frameRequest{Version: 1, CorrelationID: "tcp-invalid-" + profile, Profile: profile, Request: "{}"}
		body, err := json.Marshal(frame)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
		response, err := f.conn.Post(ctx, "/v1/coap/rpc", message.AppJSON, bytes.NewReader(body))
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		code := response.Code()
		f.conn.ReleaseMessage(response)
		if code != codes.BadRequest && code != codes.Unauthorized {
			t.Fatalf("%s invalid request code=%v", profile, code)
		}
	}
	rpcNoJobs(t, f.config.StateDir)
	if records := nativeWebTransportMessages(t, address, "#gateway-fixture"); len(records) != 0 {
		t.Fatalf("invalid request native intake=%+v", records)
	}
	registration := coapTCPObserveRegister(t, f, coapWatchRequest("tcp-token-owner"), true)
	if got := nativeObserverCount(t, statusClient); got != baseline {
		t.Fatalf("registration opened lease: %d -> %d", baseline, got)
	}
	token, err := hex.DecodeString(registration.ObservationToken)
	if err != nil || len(token) != 8 {
		t.Fatalf("registration token=%q error=%v", registration.ObservationToken, err)
	}
	fresh := f
	fresh.conn = dialCoAPTCP(t, f.address, f.cert)
	control := coapTCPRPC(t, fresh, core.Request{Version: 1, RequestID: "tcp-token-fresh-control", Operation: core.CapabilitiesGet}, true)
	if control.Result == nil || control.Result.Capabilities == nil {
		t.Fatalf("fresh TLS control=%+v", control)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	request, err := fresh.conn.NewGetRequest(ctx, "/v1/coap/observe")
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.conn.ReleaseMessage(request)
	request.SetToken(token)
	request.SetObserve(0)
	response, err := fresh.conn.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.conn.ReleaseMessage(response)
	if response.Code() != codes.BadRequest {
		t.Fatalf("stolen token response=%v", response.Code())
	}
	if got := nativeObserverCount(t, statusClient); got != baseline {
		t.Fatalf("stolen token opened lease: %d -> %d", baseline, got)
	}
}
