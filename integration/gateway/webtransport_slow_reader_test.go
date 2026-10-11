//go:build darwin || linux

package gateway_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/qlogwriter"
)

func TestWebTransportSlowReaderWriteDeadlineReleasesNativeWatch(t *testing.T) {
	pressure := make(chan quicPressureEvent, 8)
	// Stream zero carries CONNECT; the first application stream is four.
	trace := &quicPressureTrace{events: pressure, streamID: 4}
	f := newWebTransportFixtureWith(t, nil, nil, &quic.Config{
		Tracer:                         func(context.Context, bool, quic.ConnectionID) qlogwriter.Trace { return trace },
		InitialStreamReceiveWindow:     64 << 10,
		MaxStreamReceiveWindow:         64 << 10,
		InitialConnectionReceiveWindow: 64 << 10,
		MaxConnectionReceiveWindow:     64 << 10,
	})
	author := dial(t, f.config.Upstream.Address, "webtransport-slow-author", "", false, true)
	t.Cleanup(func() { _ = author.Close() })
	statusClient := nativeStatusClient(t, f.config.Upstream.Address)
	baseline := nativeObserverCount(t, statusClient)
	stream, err := f.session.OpenStreamSync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if stream.StreamID() != 4 {
		t.Fatalf("unexpected application stream ID: %d", stream.StreamID())
	}
	defer stream.Close()
	request := core.Request{Version: 1, RequestID: "webtransport-slow", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8}
	if _, err := stream.Write(webTransportFrame(t, frameRequest{Version: 1, CorrelationID: "wt-slow", Profile: "plain", Request: f.credentials.token(t, request, true), Stream: true})); err != nil {
		t.Fatal(err)
	}
	response := readWebTransportFrame(t, stream)
	if response.Event != "page" || response.Page == nil {
		t.Fatalf("WebTransport watch first response=%+v", response)
	}
	waitNativeObserverCount(t, statusClient, baseline+1)
	if err := stream.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	var event quicPressureEvent
	seeded := 0
seed:
	for seeded < 40 {
		select {
		case event = <-pressure:
			break seed
		default:
		}
		id := fmt.Sprintf("webtransport-slow-%d", seeded)
		if err := author.SendWithID("#gateway-fixture", fmt.Sprintf("webtransport-slow-%d %s", seeded, strings.Repeat("x", 3000)), id); err != nil {
			t.Fatal(err)
		}
		receipt := next(t, author, func(receipt *irc.SendReceiptEvent) bool { return receipt.RequestID == id })
		if receipt.Receipt == nil || !receipt.Receipt.Accepted {
			t.Fatalf("native pressure seed was not accepted: %+v", receipt)
		}
		seeded++
	}
	if event.at.IsZero() {
		select {
		case event = <-pressure:
		case <-time.After(5 * time.Second):
			t.Fatal("WebTransport peer did not report real flow-control pressure")
		}
	}
	if event.limit < 65536 || event.at.Before(started) {
		t.Fatalf("unexpected peer pressure: %+v", event)
	}
	t.Logf("peer blocked on %s credit at %d bytes after %d accepted native posts", event.kind, event.limit, seeded)
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) && nativeObserverCount(t, statusClient) > baseline {
		time.Sleep(50 * time.Millisecond)
	}
	elapsed := time.Since(started)
	if nativeObserverCount(t, statusClient) > baseline {
		t.Fatal("WebTransport slow reader retained native watch past write bound")
	}
	if time.Since(event.at) < 2*time.Second {
		t.Fatalf("native watch released before write deadline after peer pressure: %s", time.Since(event.at))
	}
	if f.session.Context().Err() != nil {
		t.Fatalf("client session closed before native cleanup: %v", f.session.Context().Err())
	}
	if elapsed < 2*time.Second || elapsed > 8*time.Second {
		t.Fatalf("WebTransport write-bound cleanup elapsed=%s", elapsed)
	}
}
