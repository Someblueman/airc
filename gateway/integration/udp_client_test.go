//go:build darwin || linux

package gateway_test

import (
	"crypto/rand"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/adapter"
	"github.com/Someblueman/airc/gateway/internal/core"
)

func udpDatagramCall(t *testing.T, f udpFixture, request core.Request, _ bool) adapter.FrameResponse {
	t.Helper()
	frame := frameRequest{Version: 1, CorrelationID: "udp-" + rand.Text(), Profile: "plain", Request: f.credentials.token(t, request, true)}
	writeUDPFrame(t, f, frame)
	response := readUDPResponse(t, f, time.Now().Add(5*time.Second))
	if response.Version != 1 || response.CorrelationID != frame.CorrelationID || response.Event != "result" || response.Sequence != 1 || response.Result == nil || response.Result.Operation != request.Operation {
		t.Fatalf("UDP response=%+v", response)
	}
	return response
}

func writeUDPFrame(t *testing.T, f udpFixture, frame frameRequest) {
	t.Helper()

	body, err := json.Marshal(frame)
	if err != nil || len(body) > adapter.MaxRequestFrameBytes {
		t.Fatalf("UDP request frame: %v bytes=%d", err, len(body))
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	fragments := webTransportDatagrams(body, id)
	destination, err := net.ResolveUDPAddr("udp", f.address)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for _, fragment := range fragments {
		if err := f.source.SetWriteDeadline(deadline); err != nil {
			t.Fatal(err)
		}
		if _, err := f.source.WriteToUDP(fragment, destination); err != nil {
			t.Fatal(err)
		}
	}
}
