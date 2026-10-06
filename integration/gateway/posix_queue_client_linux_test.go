//go:build linux && (arm64 || amd64)

package gateway_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func posixFrame(t *testing.T, f posixFixture, request core.Request, stream bool) adapter.FrameRequest {
	t.Helper()
	return adapter.FrameRequest{Version: 1, CorrelationID: "ug-" + request.RequestID, Profile: "encrypted", Envelope: f.credentials.token(t, request, true), Stream: stream}
}

func posixFragments(t *testing.T, frame adapter.FrameRequest) [][]byte {
	t.Helper()
	body, err := json.Marshal(frame)
	if err != nil || len(body) > adapter.MaxRequestFrameBytes {
		t.Fatalf("POSIX queue frame: %v bytes=%d", err, len(body))
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	return webTransportDatagrams(body, id)
}

func writePOSIXFragments(t *testing.T, f posixFixture, fragments [][]byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	for _, fragment := range fragments {
		if err := f.source.WriteMessage(ctx, fragment); err != nil {
			t.Fatal(err)
		}
	}
}

func readPOSIXResponse(t *testing.T, f posixFixture, deadline time.Time) adapter.FrameResponse {
	t.Helper()
	token := string(readAGD1Payload(t, &posixResponseReader{queue: f.mailbox}, deadline))
	plain, err := auth.OpenResponse(token, "udp-response", f.responseSigning, "udp-reader", f.responseRecipient, auth.ResponseScope{Realm: f.config.Realm, Principal: f.config.Principals[0].ID, Binding: f.config.Principals[0].Binding, Route: "posix-return"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var response adapter.FrameResponse
	if err := json.Unmarshal(plain, &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func posixCall(t *testing.T, f posixFixture, request core.Request) adapter.FrameResponse {
	t.Helper()
	t.Logf("POSIX queue operation=%s request_id=%s", request.Operation, request.RequestID)
	writePOSIXFragments(t, f, posixFragments(t, posixFrame(t, f, request, false)))
	response := readPOSIXResponse(t, f, time.Now().Add(5*time.Second))
	if response.Version != 1 || response.CorrelationID != "ug-"+request.RequestID || response.Event != "result" || response.Sequence != 1 || response.Result == nil || response.Result.Operation != request.Operation {
		t.Fatalf("POSIX queue response=%+v error=%+v", response, response.Error)
	}
	return response
}

func posixAccepted(t *testing.T, f posixFixture, request core.Request) core.Result {
	t.Helper()
	initial := posixCall(t, f, request)
	if initial.Result.Result == nil || !initial.Result.Result.Durable || initial.Result.Result.RequestID != request.RequestID {
		t.Fatalf("POSIX queue admission=%+v", initial)
	}
	return pollPOSIXReceipt(t, f, request.RequestID)
}

func pollPOSIXReceipt(t *testing.T, f posixFixture, id string) core.Result {
	t.Helper()
	var last core.Result
	for deadline, attempt := time.Now().Add(5*time.Second), 0; time.Now().Before(deadline); attempt++ {
		response := posixCall(t, f, core.Request{Version: 1, RequestID: fmt.Sprintf("%s-r-%d", id, attempt), Operation: core.ReceiptGet, LookupID: id})
		if response.Result.Result == nil || response.Result.Result.RequestID != id {
			t.Fatalf("POSIX queue receipt=%+v", response)
		}
		last = *response.Result.Result
		if last.State == core.Accepted {
			return last
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("POSIX queue never accepted: %+v", last)
	return core.Result{}
}

type posixResponseReader struct {
	queue    *adapter.POSIXQueue
	deadline time.Time
}

func (r *posixResponseReader) SetReadDeadline(d time.Time) error { r.deadline = d; return nil }
func (r *posixResponseReader) Read(body []byte) (int, error) {
	ctx, cancel := context.WithDeadline(context.Background(), r.deadline)
	defer cancel()
	n, _, err := r.queue.ReadMessage(ctx, body)
	return n, err
}
