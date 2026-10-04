//go:build darwin || linux

package gateway_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/quic-go/quic-go"
)

func TestRawQUICSlowReaderWriteDeadlineReleasesNativeWatch(t *testing.T) {
	f := newRawQUICFixtureWithConfig(t, false, &quic.Config{
		InitialStreamReceiveWindow:     64 << 10,
		MaxStreamReceiveWindow:         64 << 10,
		InitialConnectionReceiveWindow: 64 << 10,
		MaxConnectionReceiveWindow:     64 << 10,
	})
	author := dial(t, f.config.Bindings[0].Address, "raw-quic-slow-author", "", false, true)
	t.Cleanup(func() { _ = author.Close() })
	socket := f.config.Bindings[0].Address
	baseline := nativeObserverCount(t, socket)
	stream, err := f.conn.OpenStreamSync(t.Context())
	if err != nil {
		t.Fatal(err)
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
	waitNativeObserverCount(t, socket, baseline+1)
	started := time.Now()
	for i := 0; i < 40; i++ {
		if err := author.SendWithID("#gateway-fixture", fmt.Sprintf("raw-quic-slow-%d %s", i, strings.Repeat("x", 3000)), fmt.Sprintf("raw-quic-slow-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) && nativeObserverCount(t, socket) > baseline {
		time.Sleep(50 * time.Millisecond)
	}
	elapsed := time.Since(started)
	if nativeObserverCount(t, socket) > baseline {
		t.Fatal("raw QUIC slow reader retained native watch past write bound")
	}
	if elapsed < 2*time.Second || elapsed > 8*time.Second {
		t.Fatalf("raw QUIC write-bound cleanup elapsed=%s", elapsed)
	}
}
