//go:build darwin || linux

package gateway_test

import (
	"os"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestWebTransportShutdownJoinsActiveNativeWatch(t *testing.T) {
	f := newWebTransportFixture(t)
	baseline := nativeObserverCount(t, f.config.Bindings[0].Address)
	stream, err := f.session.OpenStreamSync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	request := core.Request{Version: 1, RequestID: "webtransport-shutdown", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8}
	frame := adapter.FrameRequest{Version: 1, CorrelationID: "wt-shutdown", Profile: "encrypted", Envelope: f.credentials.token(t, request, true), Stream: true}
	if _, err := stream.Write(webTransportFrame(t, frame)); err != nil {
		t.Fatal(err)
	}
	response := readWebTransportFrame(t, stream)
	if response.Event != "page" || response.Page == nil {
		t.Fatalf("WebTransport watch did not return first page: %+v", response)
	}
	waitNativeObserverCount(t, f.config.Bindings[0].Address, baseline+1)
	started := time.Now()
	if err := f.process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.process.done:
		if f.process.err != nil {
			t.Fatalf("gateway did not shut down cleanly: %v stderr=%s", f.process.err, f.process.stderr.String())
		}
	case <-time.After(8 * time.Second):
		t.Fatal("gateway did not join active WebTransport handler")
	}
	if elapsed := time.Since(started); elapsed > 8*time.Second {
		t.Fatalf("WebTransport shutdown took %s", elapsed)
	}
	waitNativeObserverCountAtMost(t, f.config.Bindings[0].Address, baseline)
}
