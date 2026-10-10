//go:build darwin || linux

package gateway_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func rawQUICUniCall(t *testing.T, f rawQUICFixture, request core.Request, encrypted bool) adapter.FrameResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	out, err := f.conn.OpenUniStreamSync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := out.SetWriteDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := out.Write(rawQUICFrame(t, rawQUICRequest(t, f, request, encrypted, false))); err != nil {
		out.CancelWrite(0)
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	in, err := f.conn.AcceptUniStream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Each ordinary uni response is consumed independently. Releasing its read
	// direction returns stream credit and cancels any abandoned native lease.
	defer in.CancelRead(0)
	if err := in.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	response := readRawQUICFrame(t, in)
	if response.Version != 1 || response.CorrelationID != "quic-"+request.RequestID || response.Event != "result" || response.Result == nil || response.Result.Operation != request.Operation {
		t.Fatalf("raw QUIC uni response=%+v", response)
	}
	return response
}

func rawQUICUniAccepted(t *testing.T, f rawQUICFixture, request core.Request, encrypted bool) core.Result {
	t.Helper()
	first := rawQUICAssertResult(t, rawQUICUniCall(t, f, request, encrypted), request.RequestID)
	if !first.Durable {
		t.Fatalf("raw QUIC uni admission=%+v", first)
	}
	deadline := time.Now().Add(5 * time.Second)
	var last core.Result
	for attempt := 0; time.Now().Before(deadline); attempt++ {
		receipt := core.Request{Version: 1, RequestID: fmt.Sprintf("%s-receipt-%d", request.RequestID, attempt), Operation: core.ReceiptGet, LookupID: request.RequestID}
		response := rawQUICUniCall(t, f, receipt, encrypted)
		if response.Result.Result == nil || response.Result.Result.RequestID != request.RequestID {
			t.Fatalf("raw QUIC uni receipt=%+v", response)
		}
		last = *response.Result.Result
		if last.State == core.Accepted {
			return last
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("raw QUIC uni request did not reach accepted: %+v", last)
	return core.Result{}
}
