//go:build darwin || linux

package gateway_test

import (
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
	wt "github.com/quic-go/webtransport-go"
)

func TestWebTransportSessionCloseJoinsOpenAndPartialStreams(t *testing.T) {
	t.Parallel()
	f := newWebTransportFixture(t)
	statusClient := nativeStatusClient(t, f.config.Upstream.Address)
	baseline := nativeObserverCount(t, statusClient)
	streams := make([]*wt.Stream, 0, 4)
	for i := 0; i < 3; i++ {
		stream, err := f.session.OpenStreamSync(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		streams = append(streams, stream)
		request := core.Request{Version: 1, RequestID: "session-close-capabilities-" + strconv.Itoa(i), Operation: core.CapabilitiesGet}
		frame := webTransportFrame(t, frameRequest{
			Version:       1,
			CorrelationID: "wt-session-close-" + strconv.Itoa(i),
			Profile:       "encrypted",
			Request:       f.credentials.token(t, request, true),
		})
		if _, err := stream.Write(frame); err != nil {
			t.Fatal(err)
		}
		response := readWebTransportFrame(t, stream)
		if response.Event != "result" || response.Result == nil || response.Result.Capabilities == nil {
			t.Fatalf("capability response %d=%+v", i, response)
		}
	}
	partial, err := f.session.OpenStreamSync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	streams = append(streams, partial)
	if _, err := partial.Write([]byte{0}); err != nil {
		t.Fatal(err)
	}

	closed := make(chan error, 1)
	go func() { closed <- f.session.CloseWithError(0, "client shutdown") }()
	started := time.Now()
	if err := f.process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.process.done:
		if f.process.err != nil {
			t.Fatalf("gateway shutdown failed: %v stderr=%s", f.process.err, f.process.stderr.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("gateway did not join WebTransport session after concurrent close")
	}
	if elapsed := time.Since(started); elapsed >= 2*time.Second {
		t.Fatalf("WebTransport session shutdown took %s", elapsed)
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("WebTransport client close=%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WebTransport client CloseWithError did not return")
	}
	waitNativeObserverCountAtMost(t, statusClient, baseline)
	for _, stream := range streams {
		_ = stream.Close()
	}
}
