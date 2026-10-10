//go:build darwin && arm64

package gateway_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func machCall(t *testing.T, f machIPCFixture, request core.Request) (adapter.FrameResponse, int) {
	t.Helper()
	return machCallFrame(t, f, machFrame(t, f, request))
}

func machCallFrame(t *testing.T, f machIPCFixture, frame adapter.FrameRequest) (adapter.FrameResponse, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	wire, err := runMachIPCClient(ctx, f.client, f.serviceName, machFrameBytes(t, frame))
	if err != nil {
		t.Fatalf("Mach IPC request %q: %v", frame.CorrelationID, err)
	}
	return machOpenResponse(t, f, wire, frame.CorrelationID), len(wire)
}

func machOpenResponse(t *testing.T, f machIPCFixture, wire []byte, correlation string) adapter.FrameResponse {
	t.Helper()
	token := string(wire)
	if strings.Count(token, ".") != 4 || len(wire) == 0 || len(wire) > 64<<10 {
		t.Fatalf("Mach IPC reply is not a bounded raw sealed token: %d bytes", len(wire))
	}
	if len(f.config.ReturnRoutes) != 1 {
		t.Fatalf("Mach IPC return route count=%d want=1", len(f.config.ReturnRoutes))
	}
	principal := f.config.Principals[0]
	plain, err := auth.OpenResponse(token, "mach-response", f.responseSigning, "mach-reader", f.responseRecipient,
		auth.ResponseScope{Realm: f.config.Realm, Principal: principal.ID, Binding: principal.Binding, Route: f.routeID}, time.Now())
	if err != nil {
		t.Fatalf("open protected Mach IPC reply: %v", err)
	}
	var response adapter.FrameResponse
	if err := json.Unmarshal(plain, &response); err != nil {
		t.Fatalf("decode protected Mach IPC reply: %v", err)
	}
	if response.Version != 1 || response.Sequence != 1 || response.CorrelationID != correlation {
		t.Fatalf("Mach IPC unary reply version/sequence/correlation=%d/%d/%q want 1/1/%q", response.Version, response.Sequence, response.CorrelationID, correlation)
	}
	return response
}

func machNoReply(t *testing.T, f machIPCFixture, frame adapter.FrameRequest, allowOversize bool) {
	t.Helper()
	machNoReplyWire(t, f, machFrameBytes(t, frame), allowOversize)
}

func machNoReplyWire(t *testing.T, f machIPCFixture, body []byte, allowOversize bool) {
	t.Helper()
	options := []string{"--expect-no-reply"}
	if allowOversize {
		options = append(options, "--allow-oversize")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	wire, err := runMachIPCClient(ctx, f.client, f.serviceName, body, options...)
	if err != nil || len(wire) != 0 {
		t.Fatalf("refused Mach IPC request returned a reply: bytes=%d err=%v", len(wire), err)
	}
}

func machAwaitAccepted(t *testing.T, f machIPCFixture, requestID string) core.Result {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for attempt := 0; time.Now().Before(deadline); attempt++ {
		response, _ := machCall(t, f, core.Request{Version: 1, RequestID: fmt.Sprintf("%s-receipt-%d", requestID, attempt), Operation: core.ReceiptGet, LookupID: requestID})
		if response.Event != "result" || response.Result == nil || response.Result.Result == nil || response.Result.Result.RequestID != requestID {
			t.Fatalf("Mach IPC receipt lookup=%+v", response)
		}
		if response.Result.Result.State == core.Accepted {
			return *response.Result.Result
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("Mach IPC request %q never reached native acceptance", requestID)
	return core.Result{}
}

func machAssertNoIntake(t *testing.T, f machIPCFixture) {
	t.Helper()
	rpcNoJobs(t, f.config.StateDir)
	if messages := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture"); len(messages) != 0 {
		t.Fatalf("refused Mach IPC request reached native AIRC: %+v", messages)
	}
}
