//go:build darwin && arm64

package gateway_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

type xpcFixture struct {
	machIPCFixture
	app    string
	client string
}

func newXPCFixture(t *testing.T, callerUID uint32, callerPrincipal string) xpcFixture {
	t.Helper()
	f := newMachIPCFixture(t, callerUID, callerPrincipal)
	app, client := buildXPCBundle(t, f.serviceName)
	return xpcFixture{machIPCFixture: f, app: app, client: client}
}

func xpcCall(t *testing.T, f xpcFixture, request core.Request) (adapter.FrameResponse, int) {
	t.Helper()
	return xpcCallFrame(t, f, machFrame(t, f.machIPCFixture, request))
}

func xpcCallFrame(t *testing.T, f xpcFixture, frame adapter.FrameRequest) (adapter.FrameResponse, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	wire, err := runXPCClient(ctx, f.client, machFrameBytes(t, frame), false)
	if err != nil {
		t.Fatalf("XPC request %q: %v", frame.CorrelationID, err)
	}
	return machOpenResponse(t, f.machIPCFixture, wire, frame.CorrelationID), len(wire)
}

func xpcNoReply(t *testing.T, f xpcFixture, frame adapter.FrameRequest) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	wire, err := runXPCClient(ctx, f.client, machFrameBytes(t, frame), false)
	if err == nil || len(wire) != 0 {
		t.Fatalf("refused XPC request returned a response: bytes=%d err=%v", len(wire), err)
	}
}

func xpcCallerUID() uint32 { return uint32(os.Geteuid()) }
