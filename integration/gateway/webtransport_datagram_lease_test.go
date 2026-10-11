//go:build darwin || linux

package gateway_test

import (
	"context"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestWebTransportQuietDatagramWatchEndsWithContiguousSequence(t *testing.T) {
	lease := shortGatewayTimer(t, readLeaseTimer)
	f := newWebTransportFixture(t)
	statusClient := nativeStatusClient(t, f.config.Upstream.Address)
	baseline := nativeObserverCount(t, statusClient)
	request := core.Request{Version: 1, RequestID: "datagram-quiet", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8}
	frame := adapterFrameRequest(t, f, request, true)
	frame.Stream = true
	var id [16]byte
	copy(id[:], "datagram-quiet-id")
	started := time.Now()
	for _, fragment := range webTransportDatagrams(webTransportFrame(t, frame)[4:], id) {
		if err := f.session.SendDatagram(fragment); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), lease+10*time.Second)
	defer cancel()
	first := receiveWebTransportDatagramFrame(t, f, ctx)
	if first.Event != "page" || first.Sequence != 1 || first.Page == nil {
		t.Fatalf("quiet datagram initial=%+v", first)
	}
	waitNativeObserverCount(t, statusClient, baseline+1)
	expected := uint64(2)
	for {
		response := receiveWebTransportDatagramFrame(t, f, ctx)
		if response.Sequence != expected {
			t.Fatalf("quiet datagram sequence=%d want=%d", response.Sequence, expected)
		}
		expected++
		if response.Event == "end" {
			if response.Reason != "lease_expired" {
				t.Fatalf("quiet datagram terminal=%+v", response)
			}
			break
		}
		if response.Event != "page" || response.Page == nil || len(response.Page.Messages) != 0 || len(response.Page.Events) != 0 || response.Page.Cursor != first.Page.Cursor {
			t.Fatalf("quiet datagram heartbeat=%+v", response)
		}
	}
	if elapsed := time.Since(started); elapsed < lease-2*time.Second || elapsed > lease+3*time.Second {
		t.Fatalf("quiet datagram lease=%s", elapsed)
	}
	waitNativeObserverCountAtMost(t, statusClient, baseline)
	assertWebTransportSessionUsable(t, f, "datagram-quiet-reuse")
}

func assertWebTransportSessionUsable(t *testing.T, f webTransportFixture, id string) {
	t.Helper()
	result := webTransportBidiCall(t, f, core.Request{Version: 1, RequestID: id, Operation: core.CapabilitiesGet}, true)
	if result.Capabilities == nil || len(result.Capabilities.Operations) == 0 {
		t.Fatalf("WebTransport session unusable after cancellation: %+v", result)
	}
}
