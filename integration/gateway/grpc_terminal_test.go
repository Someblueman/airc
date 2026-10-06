//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	v1 "github.com/Someblueman/airc/api/gateway/v1"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"google.golang.org/grpc/metadata"
)

func TestGRPCQuietStreamTerminalAndNativeLeaseCleanup(t *testing.T) {
	f := newGRPCGatewayFixture(t)
	seedPinMutationMessage(t, f.config.Bindings[0].Address, "#gateway-fixture", "grpc-terminal-parent")
	statusClient := nativeStatusClient(t, f.config.Bindings[0].Address)
	baseline := nativeObserverCount(t, statusClient)
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+f.token)
	stream, err := f.client.Watch(ctx, &v1.Request{Envelope: grpcEnvelope(t, core.Request{Version: 1, RequestID: "grpc-terminal", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8})})
	if err != nil {
		t.Fatal(err)
	}
	frame, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	page := grpcPage(t, frame)
	if page.Cursor == "" || len(page.Messages) != 1 {
		t.Fatalf("initial page=%+v", page)
	}
	waitNativeObserverCount(t, statusClient, baseline+1)
	lastCursor := page.Cursor
	for i := 0; i < 12; i++ {
		frame, err = stream.Recv()
		if err != nil {
			t.Fatal(err)
		}
		if terminal := frame.GetTerminal(); terminal != nil {
			var final backend.HistoryPage
			if terminal.Error != nil || terminal.Reason != "lease_expired" || json.Unmarshal(terminal.PageJson, &final) != nil || final.Cursor != lastCursor {
				t.Fatalf("terminal=%+v", terminal)
			}
			if _, err = stream.Recv(); err != io.EOF {
				t.Fatalf("terminal did not end stream: %v", err)
			}
			waitNativeObserverCountAtMost(t, statusClient, baseline)
			return
		}
		page = grpcPage(t, frame)
		if page.Cursor == "" || len(page.Messages) != 0 {
			t.Fatalf("quiet page=%+v", page)
		}
		lastCursor = page.Cursor
	}
	t.Fatal("stream exceeded bounded quiet page count")
}
