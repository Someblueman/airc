//go:build linux && arm64

package gateway_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func sctpFrame(t *testing.T, f sctpFixture, request core.Request, encrypted bool) adapter.FrameRequest {
	t.Helper()
	profile := "signed"
	if encrypted {
		profile = "encrypted"
	}
	envelope := f.credentials.token(t, request, encrypted)
	correlation := "sctp-" + request.RequestID
	if f.unordered {
		digest := sha256.Sum256([]byte(envelope))
		correlation = hex.EncodeToString(digest[:])
	}
	return adapter.FrameRequest{Version: 1, CorrelationID: correlation, Profile: profile, Envelope: envelope}
}

func sctpRequestBytes(t *testing.T, frame adapter.FrameRequest) []byte {
	t.Helper()
	body, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) == 0 || len(body) > sctpTestMessageLimit {
		t.Fatalf("SCTP FrameRequest bytes=%d exceeds one-record limit %d", len(body), sctpTestMessageLimit)
	}
	return body
}

func sctpCall(t *testing.T, f sctpFixture, conn *sctpTestConn, request core.Request) adapter.FrameResponse {
	t.Helper()
	response, _ := sctpCallMeasured(t, f, conn, request)
	return response
}

func sctpCallMeasured(t *testing.T, f sctpFixture, conn *sctpTestConn, request core.Request) (adapter.FrameResponse, int) {
	t.Helper()
	frame := sctpSendRequest(t, f, conn, request)
	response, wireBytes := sctpReadResponseMeasured(t, f, conn, frame.CorrelationID, time.Now().Add(8*time.Second))
	if response.Event != "result" || response.Sequence != 1 || response.Result == nil || response.Result.Operation != request.Operation {
		t.Fatalf("SCTP response=%+v error=%+v", response, response.Error)
	}
	return response, wireBytes
}

func sctpSendRequest(t *testing.T, f sctpFixture, conn *sctpTestConn, request core.Request) adapter.FrameRequest {
	t.Helper()
	frame := sctpFrame(t, f, request, true)
	sctpSendFrame(t, conn, frame)
	return frame
}

func sctpSendFrame(t *testing.T, conn *sctpTestConn, frame adapter.FrameRequest) {
	t.Helper()
	if err := conn.sendRecord(t, sctpRequestBytes(t, frame), time.Now().Add(5*time.Second)); err != nil {
		t.Fatalf("send SCTP request: %v", err)
	}
}

func sctpReadResponse(t *testing.T, f sctpFixture, conn *sctpTestConn, correlation string, deadline time.Time) adapter.FrameResponse {
	t.Helper()
	response, _ := sctpReadResponseMeasured(t, f, conn, correlation, deadline)
	return response
}

func sctpReadResponseMeasured(t *testing.T, f sctpFixture, conn *sctpTestConn, correlation string, deadline time.Time) (adapter.FrameResponse, int) {
	t.Helper()
	wire, err := conn.readRecord(t, deadline)
	if err != nil {
		t.Fatalf("read SCTP response record: %v", err)
	}
	response := sctpDecodeResponse(t, f, wire, correlation)
	return response, len(wire)
}

func sctpReadAnyResponse(t *testing.T, f sctpFixture, conn *sctpTestConn, deadline time.Time) (adapter.FrameResponse, int) {
	t.Helper()
	wire, err := conn.readRecord(t, deadline)
	if err != nil {
		t.Fatalf("read SCTP response record: %v", err)
	}
	return sctpDecodeResponse(t, f, wire, ""), len(wire)
}

func sctpDecodeResponse(t *testing.T, f sctpFixture, wire []byte, correlation string) adapter.FrameResponse {
	t.Helper()
	token := string(wire)
	if json.Valid(wire) || strings.Count(token, ".") != 4 {
		t.Fatalf("SCTP response is not a raw sealed compact token: %q", token)
	}
	if len(f.config.ReturnRoutes) != 1 {
		t.Fatalf("SCTP fixture return route count=%d want=1", len(f.config.ReturnRoutes))
	}
	plain, err := auth.OpenResponse(token, "sctp-response", f.responseSigning, "sctp-reader", f.responseRecipient, auth.ResponseScope{Realm: f.config.Realm, Principal: f.config.Principals[0].ID, Binding: f.config.Principals[0].Binding, Route: f.config.ReturnRoutes[0].ID}, time.Now())
	if err != nil {
		t.Fatalf("open SCTP response token: %v", err)
	}
	var response adapter.FrameResponse
	if err := json.Unmarshal(plain, &response); err != nil {
		t.Fatalf("decode SCTP response payload: %v", err)
	}
	if response.Version != 1 || (correlation != "" && response.CorrelationID != correlation) {
		t.Fatalf("SCTP response correlation=%q version=%d want %q", response.CorrelationID, response.Version, correlation)
	}
	return response
}
