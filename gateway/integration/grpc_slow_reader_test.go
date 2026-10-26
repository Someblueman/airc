//go:build darwin || linux

package gateway_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	v1 "github.com/Someblueman/airc/gateway/api/v1"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func TestGRPCSlowReaderWriteDeadlineReleasesNativeWatch(t *testing.T) {
	f := newGRPCGatewayFixture(t)
	author := dial(t, f.config.Upstream.Address, "grpc-slow-author", "", false, true)
	t.Cleanup(func() { _ = author.Close() })
	statusClient := nativeStatusClient(t, f.config.Upstream.Address)
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
	stream, err := v1.NewGatewayClient(conn).Watch(ctx, &v1.Request{RequestJson: grpcRequestJSON(t, core.Request{Version: 1, RequestID: "grpc-slow", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8})})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = stream.Recv(); err != nil {
		t.Fatal(err)
	}
	waitNativeObserverCount(t, statusClient, baseline+1)
	started := time.Now()
	var event grpcPressureEvent
	seeded := 0
seed:
	for seeded < 40 {
		select {
		case event = <-pressure:
			break seed
		default:
		}
		id := fmt.Sprintf("grpc-slow-%d", seeded)
		if err := author.SendWithID("#gateway-fixture", fmt.Sprintf("slow-%d %s", seeded, strings.Repeat("x", 3000)), id); err != nil {
			t.Fatal(err)
		}
		next(t, author, func(receipt *irc.SendReceiptEvent) bool { return receipt.RequestID == id })
		seeded++
	}
	if event.at.IsZero() {
		select {
		case event = <-pressure:
		case <-time.After(5 * time.Second):
			t.Fatal("gRPC stream did not exhaust its actual HTTP/2 credit")
		}
	}
	if event.err != nil || event.pending == 0 {
		t.Fatalf("no partial gRPC message at exhausted stream window: pending=%d err=%v", event.pending, event.err)
	}
	pressureAt := event.at
	t.Logf("HTTP/2 stream credit exhausted with %d gRPC message bytes pending after %d acknowledged native posts", event.pending, seeded)
	// Send can return at the gRPC handler's write handoff before its HTTP/2
	// flush completes. A heartbeat can then renew the three-second deadline
	// while that flush is blocked. Use this test's original overall bound,
	// rather than the generic four-second observer polling bound, so both
	// immediate-next-post and quiet-heartbeat cleanup remain covered.
	deadline := started.Add(8 * time.Second)
	for nativeObserverCount(t, statusClient) > baseline {
		if !time.Now().Before(deadline) {
			t.Fatal("native observer survived the eight-second write cleanup bound")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if ctx.Err() != nil || time.Since(pressureAt) < 2*time.Second {
		t.Fatalf("cleanup did not follow real write pressure: elapsed=%s ctx=%v", time.Since(pressureAt), ctx.Err())
	}
	if elapsed := time.Since(started); elapsed < 2*time.Second || elapsed > 8*time.Second {
		t.Fatalf("write-bound cleanup elapsed=%s", elapsed)
	}
	t.Logf("native observer released %s after flow-control pressure", time.Since(pressureAt))
}
