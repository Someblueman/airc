//go:build darwin || linux

package gateway_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	v1 "github.com/Someblueman/airc/api/gateway/v1"
	"github.com/Someblueman/airc/internal/gateway/core"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestGRPCMultiplexedWatchQuotaAndReservedUnaryCapacity(t *testing.T) {
	f := newGRPCGatewayFixture(t)
	baseline := nativeObserverCount(t, f.config.Bindings[0].Address)
	var cancels []context.CancelFunc
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
	}()
	for i := 0; i < f.config.Limits.WatchesPerPrincipal; i++ {
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		cancels = append(cancels, cancel)
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+f.token)
		stream, err := f.client.Watch(ctx, &v1.Request{Envelope: grpcEnvelope(t, core.Request{Version: 1, RequestID: fmt.Sprintf("grpc-quota-%d", i), Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8})})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = stream.Recv(); err != nil {
			t.Fatal(err)
		}
	}
	waitNativeObserverCount(t, f.config.Bindings[0].Address, baseline+len(cancels))
	ctx, cancel := grpcContext(t, f.token)
	defer cancel()
	stream, err := f.client.Watch(ctx, &v1.Request{Envelope: grpcEnvelope(t, core.Request{Version: 1, RequestID: "grpc-quota-extra", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8})})
	var denied *v1.WatchFrame
	if err == nil {
		denied, err = stream.Recv()
	}
	if status.Code(err) != codes.ResourceExhausted && (err != nil || denied.GetTerminal().GetError().GetCode() != string(core.CapacityExceeded)) {
		t.Fatalf("excess watch frame=%+v err=%v", denied, err)
	}
	capabilities, err := f.client.Capabilities(ctx, &v1.Empty{})
	if err != nil || capabilities.GetError() != nil || len(capabilities.GetResultJson()) == 0 {
		t.Fatalf("watch quota starved discovery: %+v %v", capabilities, err)
	}
	for _, cancel := range cancels {
		cancel()
	}
	waitNativeObserverCount(t, f.config.Bindings[0].Address, baseline)
	rpcNoJobs(t, f.config.StateDir)
}
