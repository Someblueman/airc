//go:build darwin || linux

package gateway_test

import (
	"context"
	"os"
	"testing"
	"time"

	v1 "github.com/Someblueman/airc/api/gateway/v1"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestGRPCGatewayShutdownJoinsActiveNativeWatch(t *testing.T) {
	f := newGRPCGatewayFixture(t)
	statusClient := nativeStatusClient(t, f.config.Upstream.Address)
	baseline := nativeObserverCount(t, statusClient)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	stream, err := f.client.Watch(ctx, &v1.Request{RequestJson: grpcRequestJSON(t, core.Request{Version: 1, RequestID: "grpc-shutdown", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8})})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = stream.Recv(); err != nil {
		t.Fatal(err)
	}
	waitNativeObserverCount(t, statusClient, baseline+1)
	if err = f.process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.process.done:
		if f.process.err != nil {
			t.Fatalf("gateway did not shut down cleanly: %v stderr=%s", f.process.err, f.process.stderr.String())
		}
	case <-time.After(8 * time.Second):
		t.Fatal("gateway did not join active gRPC handler")
	}
	waitNativeObserverCount(t, statusClient, baseline)
	if _, err = stream.Recv(); err == nil {
		t.Fatal("stream remained live after gateway exit")
	}
}
