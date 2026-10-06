//go:build darwin || linux

package gateway_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/qlogwriter"
)

func TestRawQUICSlowReaderWriteDeadlineReleasesNativeWatch(t *testing.T) {
	pressure := make(chan quicPressureEvent, 8)
	trace := &quicPressureTrace{events: pressure}
	f := newRawQUICFixtureWithConfig(t, false, &quic.Config{
		Tracer:                         func(context.Context, bool, quic.ConnectionID) qlogwriter.Trace { return trace },
		InitialStreamReceiveWindow:     64 << 10,
		MaxStreamReceiveWindow:         64 << 10,
		InitialConnectionReceiveWindow: 64 << 10,
		MaxConnectionReceiveWindow:     64 << 10,
	})
	author := dial(t, f.config.Bindings[0].Address, "raw-quic-slow-author", "", false, true)
	t.Cleanup(func() { _ = author.Close() })
	socket := f.config.Bindings[0].Address
	statusClient := nativeStatusClient(t, socket)
	baseline := nativeObserverCount(t, statusClient)
	stream, err := f.conn.OpenStreamSync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if stream.StreamID() != 0 {
		t.Fatalf("unexpected request stream ID: %d", stream.StreamID())
	}
	request := core.Request{Version: 1, RequestID: "quic-slow-watch", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8}
	if _, err := stream.Write(rawQUICFrame(t, rawQUICRequest(t, f, request, true, true))); err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	// Leave the response unread. The bounded receive window and server write
	// deadline must release the native watch lease.
	waitNativeObserverCount(t, statusClient, baseline+1)
	started := time.Now()
	for i := 0; i < 40; i++ {
		if err := author.SendWithID("#gateway-fixture", fmt.Sprintf("raw-quic-slow-%d %s", i, strings.Repeat("x", 3000)), fmt.Sprintf("raw-quic-slow-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	var event quicPressureEvent
	select {
	case event = <-pressure:
	case <-time.After(5 * time.Second):
		t.Fatal("raw QUIC peer did not report real flow-control pressure")
	}
	if event.limit < 65536 || event.at.Before(started) {
		t.Fatalf("unexpected peer pressure: %+v", event)
	}
	t.Logf("peer blocked on %s credit at %d bytes", event.kind, event.limit)
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) && nativeObserverCount(t, statusClient) > baseline {
		time.Sleep(50 * time.Millisecond)
	}
	elapsed := time.Since(started)
	if nativeObserverCount(t, statusClient) > baseline {
		t.Fatal("raw QUIC slow reader retained native watch past write bound")
	}
	if time.Since(event.at) < 2*time.Second {
		t.Fatalf("native watch released before write deadline after peer pressure: %s", time.Since(event.at))
	}
	if f.conn.Context().Err() != nil {
		t.Fatalf("client connection closed before native cleanup: %v", f.conn.Context().Err())
	}
	if elapsed < 2*time.Second || elapsed > 8*time.Second {
		t.Fatalf("raw QUIC write-bound cleanup elapsed=%s", elapsed)
	}
}
