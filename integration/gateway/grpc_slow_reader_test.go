//go:build darwin || linux

package gateway_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	v1 "github.com/Someblueman/airc/api/gateway/v1"
	"github.com/Someblueman/airc/internal/gateway/core"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
)

func TestGRPCSlowReaderWriteDeadlineReleasesNativeWatch(t *testing.T) {
	f := newGRPCGatewayFixture(t)
	author := dial(t, f.config.Bindings[0].Address, "grpc-slow-author", "", false, true)
	t.Cleanup(func() { _ = author.Close() })
	statusClient := nativeStatusClient(t, f.config.Bindings[0].Address)
	baseline := nativeObserverCount(t, statusClient)
	pressure := make(chan grpcPressureEvent, 1)
	creds := &grpcPressureCredentials{TransportCredentials: credentials.NewTLS(f.cert.Client.Clone()), events: pressure}
	conn, err := grpc.NewClient(f.address, grpc.WithTransportCredentials(creds), grpc.WithStaticStreamWindowSize(65535))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+f.token)
	stream, err := v1.NewGatewayClient(conn).Watch(ctx, &v1.Request{Envelope: grpcEnvelope(t, core.Request{Version: 1, RequestID: "grpc-slow", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8})})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = stream.Recv(); err != nil {
		t.Fatal(err)
	}
	waitNativeObserverCount(t, statusClient, baseline+1)
	started := time.Now()
	for i := 0; i < 40; i++ {
		if err := author.SendWithID("#gateway-fixture", fmt.Sprintf("slow-%d %s", i, strings.Repeat("x", 3000)), fmt.Sprintf("grpc-slow-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	var pressureAt time.Time
	select {
	case event := <-pressure:
		if event.err != nil || event.pending == 0 {
			t.Fatalf("no partial gRPC message at exhausted stream window: pending=%d err=%v", event.pending, event.err)
		}
		pressureAt = event.at
		t.Logf("HTTP/2 stream credit exhausted with %d gRPC message bytes pending", event.pending)
	case <-time.After(5 * time.Second):
		t.Fatal("gRPC stream did not exhaust its actual HTTP/2 credit")
	}
	// Stop consuming the bounded HTTP/2 stream window. The native lease must
	// close from the synchronous write bound before its ordinary 15s expiry.
	waitNativeObserverCountAtMost(t, statusClient, baseline)
	if ctx.Err() != nil || time.Since(pressureAt) < 2*time.Second {
		t.Fatalf("cleanup did not follow real write pressure: elapsed=%s ctx=%v", time.Since(pressureAt), ctx.Err())
	}
	if elapsed := time.Since(started); elapsed < 2*time.Second || elapsed > 8*time.Second {
		t.Fatalf("write-bound cleanup elapsed=%s", elapsed)
	}
}
