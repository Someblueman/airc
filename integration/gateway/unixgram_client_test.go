//go:build darwin || linux

package gateway_test

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func unixgramFrame(t *testing.T, f unixgramFixture, request core.Request, stream bool) adapter.FrameRequest {
	t.Helper()
	return adapter.FrameRequest{Version: 1, CorrelationID: "ug-" + request.RequestID, Profile: "encrypted", Envelope: f.credentials.token(t, request, true), Stream: stream}
}

func unixgramFragments(t *testing.T, frame adapter.FrameRequest) [][]byte {
	t.Helper()
	body, err := json.Marshal(frame)
	if err != nil || len(body) > adapter.MaxRequestFrameBytes {
		t.Fatalf("Unix datagram frame: %v bytes=%d", err, len(body))
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	return webTransportDatagrams(body, id)
}

func writeUnixgramFragments(t *testing.T, f unixgramFixture, fragments [][]byte) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	if err := f.source.SetWriteDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range fragments {
		for {
			n, err := f.source.WriteToUnix(fragment, &net.UnixAddr{Name: f.address, Net: "unixgram"})
			if err == nil && n == len(fragment) {
				break
			}
			if n != 0 || !errors.Is(err, syscall.ENOBUFS) || !time.Now().Before(deadline) {
				t.Fatalf("Unix datagram client write bytes=%d err=%v", n, err)
			}
			time.Sleep(time.Millisecond)
		}
	}
}

func readUnixgramResponse(t *testing.T, f unixgramFixture, deadline time.Time) adapter.FrameResponse {
	t.Helper()
	token := string(readAGD1Payload(t, f.mailbox, deadline))
	plain, err := auth.OpenResponse(token, "udp-response", f.responseSigning, "udp-reader", f.responseRecipient, auth.ResponseScope{Realm: f.config.Realm, Principal: f.config.Principals[0].ID, Binding: f.config.Principals[0].Binding, Route: "unixgram-return"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var response adapter.FrameResponse
	if err := json.Unmarshal(plain, &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func unixgramCall(t *testing.T, f unixgramFixture, request core.Request, _ bool) adapter.FrameResponse {
	t.Helper()
	t.Logf("Unix datagram operation=%s request_id=%s", request.Operation, request.RequestID)
	writeUnixgramFragments(t, f, unixgramFragments(t, unixgramFrame(t, f, request, false)))
	response := readUnixgramResponse(t, f, time.Now().Add(5*time.Second))
	if response.Version != 1 || response.CorrelationID != "ug-"+request.RequestID || response.Event != "result" || response.Sequence != 1 || response.Result == nil || response.Result.Operation != request.Operation {
		t.Fatalf("Unix datagram response=%+v error=%+v", response, response.Error)
	}
	return response
}

func unixgramAccepted(t *testing.T, f unixgramFixture, request core.Request, encrypted bool) core.Result {
	t.Helper()
	initial := unixgramCall(t, f, request, encrypted)
	if initial.Result.Result == nil || !initial.Result.Result.Durable || initial.Result.Result.RequestID != request.RequestID {
		t.Fatalf("Unix datagram admission=%+v", initial)
	}
	return pollUnixgramReceipt(t, f, request.RequestID)
}

func pollUnixgramReceipt(t *testing.T, f unixgramFixture, id string) core.Result {
	t.Helper()
	var last core.Result
	for deadline, attempt := time.Now().Add(5*time.Second), 0; time.Now().Before(deadline); attempt++ {
		response := unixgramCall(t, f, core.Request{Version: 1, RequestID: fmt.Sprintf("%s-r-%d", id, attempt), Operation: core.ReceiptGet, LookupID: id}, true)
		if response.Result.Result == nil || response.Result.Result.RequestID != id {
			t.Fatalf("Unix datagram receipt=%+v", response)
		}
		last = *response.Result.Result
		if last.State == core.Accepted {
			return last
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("Unix datagram never accepted: %+v", last)
	return core.Result{}
}
