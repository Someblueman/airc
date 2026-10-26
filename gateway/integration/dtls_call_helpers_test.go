//go:build darwin || linux

package gateway_test

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/adapter"
	"github.com/Someblueman/airc/gateway/internal/core"
)

func dtlsDatagramCall(t *testing.T, f dtlsFixture, request core.Request, encrypted bool) adapter.FrameResponse {
	t.Helper()
	frame := frameRequest{
		Version:       1,
		CorrelationID: "dtls-" + rand.Text(),
		Profile:       "plain",
		Request:       f.credentials.token(t, request, encrypted),
	}
	writeDTLSFrame(t, f, frame)
	response := readDTLSResponse(t, f.conn, time.Now().Add(5*time.Second))
	if response.Version != 1 || response.CorrelationID != frame.CorrelationID || response.Event != "result" || response.Sequence != 1 || response.Result == nil || response.Result.Operation != request.Operation {
		t.Fatalf("DTLS response=%+v error=%+v", response, response.Error)
	}
	return response
}

func dtlsDatagramAccepted(t *testing.T, f dtlsFixture, request core.Request, encrypted bool) core.Result {
	t.Helper()
	firstResponse := dtlsDatagramCall(t, f, request, encrypted)
	if firstResponse.Result == nil || firstResponse.Result.Result == nil {
		t.Fatalf("DTLS admission result=%+v", firstResponse)
	}
	first := *firstResponse.Result.Result
	if first.RequestID != request.RequestID {
		t.Fatalf("DTLS admission request id=%q want=%q", first.RequestID, request.RequestID)
	}
	if !first.Durable {
		t.Fatalf("DTLS admission=%+v", first)
	}
	attempt := 0
	return acceptedReceipt(t, 5*time.Second, 40*time.Millisecond, "the DTLS request "+request.RequestID+" to reach accepted", func() *core.Result {
		receipt := core.Request{Version: 1, RequestID: fmt.Sprintf("%s-receipt-%d", request.RequestID, attempt), Operation: core.ReceiptGet, LookupID: request.RequestID}
		attempt++
		response := dtlsDatagramCall(t, f, receipt, encrypted)
		if response.Result == nil || response.Result.Result == nil || response.Result.Result.RequestID != request.RequestID {
			t.Fatalf("DTLS receipt=%+v", response)
		}
		return response.Result.Result
	})
}

func writeDTLSFrame(t *testing.T, f dtlsFixture, frame frameRequest) {
	t.Helper()
	body, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) == 0 || len(body) > adapter.MaxRequestFrameBytes {
		t.Fatalf("DTLS request frame bytes=%d", len(body))
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	fragments := webTransportDatagrams(body, id)
	if len(fragments) == 0 {
		t.Fatal("empty DTLS datagram request")
	}
	for _, fragment := range fragments {
		if err := f.conn.SetWriteDeadline(time.Now().Add(3 * time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := f.conn.Write(fragment); err != nil {
			t.Fatal(err)
		}
	}
}
