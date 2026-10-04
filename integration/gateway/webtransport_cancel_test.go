//go:build darwin || linux

package gateway_test

import (
	"context"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func webTransportWatchRequest(t *testing.T, f webTransportFixture, id string) []byte {
	t.Helper()
	request := core.Request{
		Version:   1,
		RequestID: id,
		Operation: core.Watch,
		Target:    &core.Target{Kind: "room", Name: "#gateway-fixture"},
		Start:     "retained",
		Limit:     8,
	}
	return webTransportFrame(t, adapter.FrameRequest{
		Version:       1,
		CorrelationID: "wt-" + id,
		Profile:       "encrypted",
		Envelope:      f.credentials.token(t, request, true),
		Stream:        true,
	})
}

func waitWebTransportLeaseGone(t *testing.T, socket string, baseline int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if nativeObserverCount(t, socket) <= baseline {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("WebTransport watch lease remained after cancellation: baseline=%d current=%d", baseline, nativeObserverCount(t, socket))
}

func assertWebTransportSessionUsable(t *testing.T, f webTransportFixture, id string) {
	t.Helper()
	result := webTransportBidiCall(t, f, core.Request{Version: 1, RequestID: id, Operation: core.CapabilitiesGet}, true)
	if result.Capabilities == nil || len(result.Capabilities.Operations) == 0 {
		t.Fatalf("WebTransport session unusable after cancellation: %+v", result)
	}
}

func TestWebTransportCancelReadStopsBidiWatchAndKeepsSessionUsable(t *testing.T) {
	f := newWebTransportFixture(t)
	baseline := nativeObserverCount(t, f.config.Bindings[0].Address)
	stream, err := f.session.OpenStreamSync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Write(webTransportWatchRequest(t, f, "cancel-bidi")); err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	page := readWebTransportFrame(t, stream)
	if page.Event != "page" || page.Page == nil {
		t.Fatalf("WebTransport bidi watch initial page=%+v", page)
	}
	waitNativeObserverCount(t, f.config.Bindings[0].Address, baseline+1)
	stream.CancelRead(0)
	waitWebTransportLeaseGone(t, f.config.Bindings[0].Address, baseline)
	assertWebTransportSessionUsable(t, f, "cancel-bidi-capabilities")
	_ = stream.Close()
}

func TestWebTransportCancelReadStopsUniWatchAndKeepsSessionUsable(t *testing.T) {
	f := newWebTransportFixture(t)
	baseline := nativeObserverCount(t, f.config.Bindings[0].Address)
	out, err := f.session.OpenUniStreamSync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := out.Write(webTransportWatchRequest(t, f, "cancel-uni")); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	in, err := f.session.AcceptUniStream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	page := readWebTransportFrame(t, in)
	if page.Event != "page" || page.Page == nil {
		t.Fatalf("WebTransport uni watch initial page=%+v", page)
	}
	waitNativeObserverCount(t, f.config.Bindings[0].Address, baseline+1)
	in.CancelRead(0)
	waitWebTransportLeaseGone(t, f.config.Bindings[0].Address, baseline)
	assertWebTransportSessionUsable(t, f, "cancel-uni-capabilities")
}
