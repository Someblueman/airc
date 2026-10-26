//go:build darwin || linux

package gateway_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/internal/testwait"
	"github.com/Someblueman/airc/pkg/irc"
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
	author := dial(t, f.config.Upstream.Address, "raw-quic-slow-author", "", false, true)
	t.Cleanup(func() { _ = author.Close() })
	socket := f.config.Upstream.Address
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
	var event quicPressureEvent
	seeded := 0
seed:
	for seeded < 40 {
		select {
		case event = <-pressure:
			break seed
		default:
		}
		id := fmt.Sprintf("raw-quic-slow-%d", seeded)
		if err := author.SendWithID("#gateway-fixture", fmt.Sprintf("raw-quic-slow-%d %s", seeded, strings.Repeat("x", 3000)), id); err != nil {
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
			t.Fatal("raw QUIC peer did not report real flow-control pressure")
		}
	}
	if event.limit < 65536 || event.at.Before(started) {
		t.Fatalf("unexpected peer pressure: %+v", event)
	}
	t.Logf("peer blocked on %s credit at %d bytes after %d accepted native posts", event.kind, event.limit, seeded)
	testwait.Every(t, 8*time.Second, 50*time.Millisecond, "the raw QUIC slow reader's native watch to be released within the write bound", func() bool {
		return nativeObserverCount(t, statusClient) <= baseline
	})
	elapsed := time.Since(started)
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
