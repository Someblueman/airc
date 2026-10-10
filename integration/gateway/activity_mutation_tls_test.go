//go:build darwin || linux

package gateway_test

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestActivityMutationOverFramedTLS(t *testing.T) {
	conn, credentials, stop, socket := framedTLSCardFixture(t)
	defer conn.Close()
	defer stop()
	observer := newActivityObserver(t, socket, core.Target{Kind: "room", Name: "#gateway-fixture"})
	call := func(request core.Request, correlation string) adapter.FrameResponse {
		writeFrame(t, conn, adapter.FrameRequest{Version: 1, CorrelationID: correlation, Profile: "encrypted", Envelope: credentials.token(t, request, true)})
		response := readFrame(t, conn)
		if response.CorrelationID != correlation || response.Event != "result" {
			t.Fatalf("TLS activity response mismatch: %+v", response)
		}
		return response
	}
	request := activityRequest("activity-tls", "thinking", "TLS thinking", 5)
	response := call(request, request.RequestID)
	if response.Error != nil || response.Result == nil {
		t.Fatalf("TLS activity admission: %+v", response)
	}
	assertActivityMutationFacts(t, waitActivityResult(t, call, request), request, nextActivitySignal(t, observer))
}
