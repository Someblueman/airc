//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/json"
	"testing"

	v1 "github.com/Someblueman/airc/gateway/api/v1"
	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/gateway/internal/core"
)

func grpcPage(t *testing.T, frame *v1.WatchFrame) backend.HistoryPage {
	t.Helper()
	page := frame.GetPageJson()
	if len(page) == 0 {
		t.Fatalf("gRPC watch frame was not a page: %+v", frame)
	}
	var result backend.HistoryPage
	if err := json.Unmarshal(page, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestGRPCWatchRetainedLiveAndCancellation(t *testing.T) {
	f := newGRPCGatewayFixture(t)
	parentID := seedPinMutationMessage(t, f.config.Upstream.Address, "#gateway-fixture", "grpc-watch-parent")
	author := dial(t, f.config.Upstream.Address, "grpc-watch-author", "", false, true)
	t.Cleanup(func() { _ = author.Close() })
	statusClient := nativeStatusClient(t, f.config.Upstream.Address)
	baseline := nativeObserverCount(t, statusClient)
	ctx, cancel := context.WithCancel(grpcContextValue(t, f.token))
	defer cancel()
	stream, err := f.client.Watch(ctx, &v1.Request{RequestJson: grpcRequestJSON(t, core.Request{Version: 1, RequestID: "grpc-watch", Operation: core.EventsWatch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8})})
	if err != nil {
		t.Fatal(err)
	}
	waitNativeObserverCount(t, statusClient, baseline+1)
	initialFrame, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	initial := grpcPage(t, initialFrame)
	if initial.Cursor == "" || len(initial.Messages) == 0 || initial.Messages[0] == nil || initial.Messages[0].ID != parentID {
		t.Fatalf("gRPC retained page=%+v", initial)
	}
	triggerLiveSignal(t, author, "typing")
	for {
		frame, recvErr := stream.Recv()
		if recvErr != nil {
			t.Fatal(recvErr)
		}
		page := grpcPage(t, frame)
		if len(page.Events) == 0 {
			continue
		}
		if page.Cursor != initial.Cursor || page.Events[0] == nil || page.Events[0].Signal == nil {
			t.Fatalf("gRPC live page changed cursor/lost signal: initial=%+v live=%+v", initial, page)
		}
		break
	}
	cancel()
	waitNativeObserverCountAtMost(t, statusClient, baseline)
}

func grpcContextValue(t *testing.T, token string) context.Context {
	ctx, cancel := grpcContext(t, token)
	t.Cleanup(cancel)
	return ctx
}
