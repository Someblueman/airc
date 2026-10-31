//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/adapter"
	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/net/blockwise"
	coapNetClient "github.com/plgd-dev/go-coap/v3/net/client"
	coapOptions "github.com/plgd-dev/go-coap/v3/options"
	coapUDP "github.com/plgd-dev/go-coap/v3/udp"
	coapClient "github.com/plgd-dev/go-coap/v3/udp/client"
)

// coapFixture is a gateway with one CoAP over UDP listener and a go-coap
// client dialled to it.
type coapFixture struct {
	address     string
	config      config.Config
	credentials *detachedCredentials
	conn        *coapClient.Conn
}

func newCoAPFixture(t *testing.T) coapFixture {
	t.Helper()
	p := newPacketFixture(t, "coap_udp", nil)
	conn, err := coapUDP.Dial(p.address, coapOptions.WithBlockwise(true, blockwise.SZX1024, 5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return coapFixture{address: p.address, config: p.config, credentials: p.credentials, conn: conn}
}

func cancelCoAPObservation(observation coapNetClient.Observation) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = observation.Cancel(ctx)
}

func coapRPC(t *testing.T, f coapFixture, request core.Request, encrypted bool) adapter.FrameResponse {
	return coapRPCType(t, f, request, encrypted, message.Confirmable)
}

func coapRPCType(t *testing.T, f coapFixture, request core.Request, encrypted bool, typ message.Type) adapter.FrameResponse {
	t.Helper()
	frame := frameRequest{Version: 1, CorrelationID: "coap-" + request.RequestID, Profile: "plain", Request: f.credentials.token(t, request, encrypted)}
	body, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	req, err := f.conn.NewPostRequest(ctx, "/v1/coap/rpc", message.AppJSON, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.SetType(typ)
	response, err := f.conn.Do(req)
	f.conn.ReleaseMessage(req)
	if err != nil {
		t.Fatalf("CoAP operation=%s request_id=%s: %v", request.Operation, request.RequestID, err)
	}
	payload, err := response.ReadBody()
	if err != nil {
		f.conn.ReleaseMessage(response)
		t.Fatal(err)
	}
	if response.Code() != codes.Content {
		f.conn.ReleaseMessage(response)
		t.Fatalf("CoAP operation=%s request_id=%s code=%v payload=%q", request.Operation, request.RequestID, response.Code(), payload)
	}
	f.conn.ReleaseMessage(response)
	var result adapter.FrameResponse
	if err := json.Unmarshal(payload, &result); err != nil {
		t.Fatal(err)
	}
	if result.Version != 1 || result.CorrelationID != frame.CorrelationID || result.Event != "result" || result.Sequence != 1 || result.Result == nil || result.Result.Operation != request.Operation {
		t.Fatalf("CoAP operation=%s request_id=%s response=%+v error=%+v", request.Operation, request.RequestID, result, result.Error)
	}
	return result
}

type coapRegistration struct {
	Version          int    `json:"version"`
	CorrelationID    string `json:"correlation_id"`
	ObservationToken string `json:"observation_token"`
	ExpiresInMS      int    `json:"expires_in_ms"`
}

func coapObserveRegister(t *testing.T, f coapFixture, request core.Request, encrypted bool) coapRegistration {
	t.Helper()
	frame := frameRequest{Version: 1, CorrelationID: "coap-observe-" + request.RequestID, Profile: "plain", Request: f.credentials.token(t, request, encrypted), Stream: true}
	body, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	response, err := f.conn.Post(ctx, "/v1/coap/observe/register", message.AppJSON, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := response.ReadBody()
	if err != nil {
		t.Fatal(err)
	}
	var registration coapRegistration
	if response.Code() != codes.Content || json.Unmarshal(payload, &registration) != nil || registration.Version != 1 || registration.CorrelationID != frame.CorrelationID || len(registration.ObservationToken) != 16 || registration.ExpiresInMS != 10000 {
		t.Fatalf("CoAP observe registration code=%v payload=%q registration=%+v", response.Code(), payload, registration)
	}
	if _, err := hex.DecodeString(registration.ObservationToken); err != nil {
		t.Fatalf("CoAP observation token=%q: %v", registration.ObservationToken, err)
	}
	return registration
}

func coapWatchRequest(id string) core.Request {
	return core.Request{Version: 1, RequestID: id, Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8}
}
