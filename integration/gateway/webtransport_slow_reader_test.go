//go:build darwin || linux

package gateway_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/quic-go/quic-go"
)

func TestWebTransportSlowReaderWriteDeadlineReleasesNativeWatch(t *testing.T) {
	f := newWebTransportFixtureWith(t, nil, nil, &quic.Config{
		InitialStreamReceiveWindow:     64 << 10,
		MaxStreamReceiveWindow:         64 << 10,
		InitialConnectionReceiveWindow: 64 << 10,
		MaxConnectionReceiveWindow:     64 << 10,
	})
	author := dial(t, f.config.Bindings[0].Address, "webtransport-slow-author", "", false, true)
	t.Cleanup(func() { _ = author.Close() })
	baseline := nativeObserverCount(t, f.config.Bindings[0].Address)
	stream, err := f.session.OpenStreamSync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	request := core.Request{Version: 1, RequestID: "webtransport-slow", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8}
	if _, err := stream.Write(webTransportFrame(t, adapter.FrameRequest{Version: 1, CorrelationID: "wt-slow", Profile: "encrypted", Envelope: f.credentials.token(t, request, true), Stream: true})); err != nil {
		t.Fatal(err)
	}
	response := readWebTransportFrame(t, stream)
	if response.Event != "page" || response.Page == nil {
		t.Fatalf("WebTransport watch first response=%+v", response)
	}
	waitNativeObserverCount(t, f.config.Bindings[0].Address, baseline+1)
	if err := stream.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	for i := 0; i < 40; i++ {
		if err := author.SendWithID("#gateway-fixture", fmt.Sprintf("webtransport-slow-%d %s", i, strings.Repeat("x", 3000)), fmt.Sprintf("webtransport-slow-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) && nativeObserverCount(t, f.config.Bindings[0].Address) > baseline {
		time.Sleep(50 * time.Millisecond)
	}
	elapsed := time.Since(started)
	if nativeObserverCount(t, f.config.Bindings[0].Address) > baseline {
		t.Fatal("WebTransport slow reader retained native watch past write bound")
	}
	if elapsed < 2*time.Second || elapsed > 8*time.Second {
		t.Fatalf("WebTransport write-bound cleanup elapsed=%s", elapsed)
	}
}
