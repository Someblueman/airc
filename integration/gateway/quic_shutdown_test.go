//go:build darwin || linux

package gateway_test

import (
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestRawQUICActiveWatchOpenAndPartialStreamsJoinOnSIGINT(t *testing.T) {
	for _, datagrams := range []bool{false, true} {
		t.Run(fmt.Sprintf("datagrams-%t", datagrams), func(t *testing.T) {
			f := newRawQUICFixture(t, datagrams)
			baseline := nativeObserverCount(t, f.config.Bindings[0].Address)
			for i := 0; i < 2; i++ {
				stream, err := f.conn.OpenStreamSync(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				request := core.Request{Version: 1, RequestID: fmt.Sprintf("quic-open-%d", i), Operation: core.CapabilitiesGet}
				if _, err := stream.Write(rawQUICFrame(t, rawQUICRequest(t, f, request, true, false))); err != nil {
					t.Fatal(err)
				}
				response := readRawQUICFrame(t, stream)
				if response.Event != "result" || response.Result == nil || response.Result.Capabilities == nil {
					t.Fatalf("open stream response=%+v", response)
				}
			}
			partial, err := f.conn.OpenStreamSync(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := partial.Write([]byte{0}); err != nil {
				t.Fatal(err)
			}
			watch, err := f.conn.OpenStreamSync(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := watch.Write(rawQUICWatchFrame(t, f, "quic-shutdown-watch")); err != nil {
				t.Fatal(err)
			}
			if err := watch.Close(); err != nil {
				t.Fatal(err)
			}
			page := readRawQUICFrame(t, watch)
			if page.Event != "page" || page.Page == nil {
				t.Fatalf("shutdown watch=%+v", page)
			}
			waitNativeObserverCount(t, f.config.Bindings[0].Address, baseline+1)
			if err := f.process.command.Process.Signal(os.Interrupt); err != nil {
				t.Fatal(err)
			}
			select {
			case <-f.process.done:
				if f.process.err != nil {
					t.Fatalf("raw QUIC SIGINT failed: %v stderr=%s", f.process.err, f.process.stderr.String())
				}
			case <-time.After(2 * time.Second):
				t.Fatal("raw QUIC SIGINT failed to join streams/watch")
			}
			// QUIC's CONNECTION_CLOSE packet is not delivery-guaranteed when the
			// owned UDP transport closes. Verify server resources directly instead
			// of requiring the peer to observe that packet before its idle timeout.
			packet, err := net.ListenPacket("udp", f.address)
			if err != nil {
				t.Fatalf("gateway retained UDP socket after SIGINT: %v", err)
			}
			_ = packet.Close()
			waitNativeLeaseGone(t, f.config.Bindings[0].Address, baseline)
		})
	}
}
