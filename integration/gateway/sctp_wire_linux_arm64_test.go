//go:build linux && arm64

package gateway_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func sctpFrame(t *testing.T, f sctpFixture, request core.Request, encrypted bool) frameRequest {
	t.Helper()
	profile := "plain"
	requestBody := f.credentials.token(t, request, encrypted)
	digest := sha256.Sum256([]byte(requestBody))
	return frameRequest{Version: 1, CorrelationID: hex.EncodeToString(digest[:]), Profile: profile, Request: requestBody}
}

func sctpRequestBytes(t *testing.T, frame frameRequest) []byte {
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

func sctpSendRequest(t *testing.T, f sctpFixture, conn *sctpTestConn, request core.Request) frameRequest {
	t.Helper()
	frame := sctpFrame(t, f, request, true)
	sctpSendFrame(t, conn, frame)
	return frame
}

func sctpSendFrame(t *testing.T, conn *sctpTestConn, frame frameRequest) {
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
	var response adapter.FrameResponse
	if err := json.Unmarshal(wire, &response); err != nil {
		t.Fatalf("decode SCTP plain response frame: %v", err)
	}
	if response.Version != 1 || (correlation != "" && response.CorrelationID != correlation) {
		t.Fatalf("SCTP response correlation=%q version=%d want %q", response.CorrelationID, response.Version, correlation)
	}
	return response
}
