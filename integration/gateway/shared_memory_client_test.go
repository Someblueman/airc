//go:build (darwin || linux) && arm64

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

func sharedMemoryFrame(t *testing.T, f nativeQueueFixture, request core.Request, stream bool) adapter.FrameRequest {
	t.Helper()
	return adapter.FrameRequest{Version: 1, CorrelationID: "ug-" + request.RequestID, Profile: "encrypted", Envelope: f.credentials.token(t, request, true), Stream: stream}
}

func sharedMemoryFragments(t *testing.T, frame adapter.FrameRequest) [][]byte {
	t.Helper()
	body, err := json.Marshal(frame)
	if err != nil || len(body) > adapter.MaxRequestFrameBytes {
		t.Fatalf("mapped queue frame: %v bytes=%d", err, len(body))
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	return webTransportDatagrams(body, id)
}

func writeSharedMemoryFragments(t *testing.T, f nativeQueueFixture, fragments [][]byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	for _, fragment := range fragments {
		if err := f.source.WriteMessage(ctx, fragment); err != nil {
			t.Fatal(err)
		}
	}
}

func readSharedMemoryResponse(t *testing.T, f nativeQueueFixture, deadline time.Time) adapter.FrameResponse {
	t.Helper()
	token := string(readAGD1Payload(t, &sharedMemoryResponseReader{queue: f.mailbox}, deadline))
	plain, err := auth.OpenResponse(token, "udp-response", f.responseSigning, "udp-reader", f.responseRecipient, auth.ResponseScope{Realm: f.config.Realm, Principal: f.config.Principals[0].ID, Binding: f.config.Principals[0].Binding, Route: f.kind + "-return"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var response adapter.FrameResponse
	if err := json.Unmarshal(plain, &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func sharedMemoryCall(t *testing.T, f nativeQueueFixture, request core.Request) adapter.FrameResponse {
	t.Helper()
	t.Logf("%s queue operation=%s request_id=%s", f.kind, request.Operation, request.RequestID)
	writeSharedMemoryFragments(t, f, sharedMemoryFragments(t, sharedMemoryFrame(t, f, request, false)))
	response := readSharedMemoryResponse(t, f, time.Now().Add(5*time.Second))
	if response.Version != 1 || response.CorrelationID != "ug-"+request.RequestID || response.Event != "result" || response.Sequence != 1 || response.Result == nil || response.Result.Operation != request.Operation {
		t.Fatalf("%s queue response=%+v error=%+v", f.kind, response, response.Error)
	}
	return response
}

func sharedMemoryAccepted(t *testing.T, f nativeQueueFixture, request core.Request) core.Result {
	t.Helper()
	initial := sharedMemoryCall(t, f, request)
	if initial.Result.Result == nil || !initial.Result.Result.Durable || initial.Result.Result.RequestID != request.RequestID {
		t.Fatalf("%s queue admission=%+v", f.kind, initial)
	}
	return pollSharedMemoryReceipt(t, f, request.RequestID)
}

func pollSharedMemoryReceipt(t *testing.T, f nativeQueueFixture, id string) core.Result {
	t.Helper()
	var last core.Result
	for deadline, attempt := time.Now().Add(5*time.Second), 0; time.Now().Before(deadline); attempt++ {
		response := sharedMemoryCall(t, f, core.Request{Version: 1, RequestID: fmt.Sprintf("%s-r-%d", id, attempt), Operation: core.ReceiptGet, LookupID: id})
		if response.Result.Result == nil || response.Result.Result.RequestID != id {
			t.Fatalf("%s queue receipt=%+v", f.kind, response)
		}
		last = *response.Result.Result
		if last.State == core.Accepted {
			return last
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("%s queue never accepted: %+v", f.kind, last)
	return core.Result{}
}

type sharedMemoryResponseReader struct {
	queue    *adapter.MappedQueue
	deadline time.Time
}

func (r *sharedMemoryResponseReader) SetReadDeadline(d time.Time) error { r.deadline = d; return nil }
func (r *sharedMemoryResponseReader) Read(body []byte) (int, error) {
	ctx, cancel := context.WithDeadline(context.Background(), r.deadline)
	defer cancel()
	n, err := r.queue.ReadMessage(ctx, body)
	return n, err
}
