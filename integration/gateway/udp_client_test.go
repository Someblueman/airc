//go:build darwin || linux

package gateway_test

import (
	"crypto/rand"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func udpDatagramCall(t *testing.T, f udpFixture, request core.Request, _ bool) adapter.FrameResponse {
	t.Helper()
	frame := adapter.FrameRequest{Version: 1, CorrelationID: "udp-" + rand.Text(), Profile: "encrypted", Envelope: f.credentials.token(t, request, true)}
	writeUDPFrame(t, f, frame)
	response := readUDPResponse(t, f, time.Now().Add(5*time.Second))
	if response.Version != 1 || response.CorrelationID != frame.CorrelationID || response.Event != "result" || response.Sequence != 1 || response.Result == nil || response.Result.Operation != request.Operation {
		t.Fatalf("UDP response=%+v", response)
	}
	return response
}

func udpDatagramAccepted(t *testing.T, f udpFixture, request core.Request, encrypted bool) core.Result {
	t.Helper()
	initial := udpDatagramCall(t, f, request, encrypted)
	if initial.Result == nil || initial.Result.Result == nil || initial.Result.Result.RequestID != request.RequestID || !initial.Result.Result.Durable {
		t.Fatalf("UDP admission=%+v", initial)
	}
	deadline := time.Now().Add(5 * time.Second)
	var last core.Result
	for attempt := 0; time.Now().Before(deadline); attempt++ {
		receipt := core.Request{Version: 1, RequestID: request.RequestID + "-receipt-" + string(rune('a'+attempt%26)), Operation: core.ReceiptGet, LookupID: request.RequestID}
		response := udpDatagramCall(t, f, receipt, true)
		if response.Result == nil || response.Result.Result == nil || response.Result.Result.RequestID != request.RequestID {
			t.Fatalf("UDP receipt=%+v", response)
		}
		last = *response.Result.Result
		if last.State == core.Accepted {
			return last
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("UDP request did not reach accepted: %+v", last)
	return core.Result{}
}

func writeUDPFrame(t *testing.T, f udpFixture, frame adapter.FrameRequest) {
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
