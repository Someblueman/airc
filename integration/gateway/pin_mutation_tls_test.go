//go:build darwin || linux

package gateway_test

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestPinMutationOverFramedTLS(t *testing.T) {
	conn, credentials, stop, socket := framedTLSCardFixture(t)
	defer conn.Close()
	defer stop()
	parent := seedPinMutationMessage(t, socket, "#gateway-fixture", "pin-tls-parent")
	call := func(request core.Request, correlation string) adapter.FrameResponse {
		writeFrame(t, conn, frameRequest{
			Version: 1, CorrelationID: correlation, Profile: "plain",
			Request: credentials.token(t, request, true),
		})
		response := readFrame(t, conn)
		if response.CorrelationID != correlation || response.Event != "result" {
			t.Fatalf("TLS pin response mismatch: %+v", response)
		}
		return response
	}
	add := pinRequest("pin-tls-add", core.PinsAdd, parent)
	if response := call(add, add.RequestID); response.Error != nil || response.Result == nil {
		t.Fatalf("TLS pin add admission: %+v", response)
	}
	assertPinMutationFacts(t, waitFramePinResult(t, call, add), add)
	if pins := queryPinMutationNative(t, socket, "#gateway-fixture"); len(pins) != 1 || pins[0].ID != parent {
		t.Fatalf("TLS native pin missing: %v", pins)
	}
	remove := pinRequest("pin-tls-remove", core.PinsRemove, parent)
	if response := call(remove, remove.RequestID); response.Error != nil || response.Result == nil {
		t.Fatalf("TLS pin remove admission: %+v", response)
	}
	assertPinMutationFacts(t, waitFramePinResult(t, call, remove), remove)
	if pins := queryPinMutationNative(t, socket, "#gateway-fixture"); len(pins) != 0 {
		t.Fatalf("TLS native unpin missing: %v", pins)
	}
}
