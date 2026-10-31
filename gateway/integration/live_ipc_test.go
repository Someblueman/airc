//go:build darwin || linux

package gateway_test

import (
	"os"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/adapter"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestEventsWatchUnixStreamReturnsNativeSignal(t *testing.T) {
	t.Parallel()
	conn, credentials, stop, _, upstream := unixStreamFixture(t)
	defer conn.Close()
	author := dial(t, upstream, "live-unix-author", "", false, true)
	defer author.Close()
	parentID := seedPinMutationMessage(t, upstream, "#gateway-fixture", "live-unix-parent")
	request := core.Request{Version: 1, RequestID: "live-unix", Operation: core.EventsWatch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Limit: 8}
	writeFrame(t, conn, liveFrame(t, request, credentials, "encrypted"))
	initial := readFrame(t, conn)
	if initial.Event != "page" || initial.Page == nil || len(initial.Page.Messages) != 1 || initial.Page.Cursor == "" || len(initial.Page.Events) != 0 {
		t.Fatalf("Unix live initial page: %+v", initial)
	}
	triggerLiveChat(t, author, irc.ChatRequest{Action: "prepare", Target: "#gateway-fixture", ID: parentID, Seconds: 10, Text: "live prepare"})
	readLiveFrameEvent(t, func() adapter.FrameResponse { return readFrame(t, conn) }, "live-live-unix", "prepare", initial.Page.Cursor)
	stop()
}

func TestEventsWatchStdioReturnsNativeSignals(t *testing.T) {
	t.Parallel()
	for _, profile := range []string{"signed", "encrypted"} {
		t.Run(profile, func(t *testing.T) {
			socket, config, credentials, _ := stdioFixtureConfig(t)
			p := startStdio(t, gatewayBinary(t), config)
			author := dial(t, socket, "live-stdio-author", "", false, true)
			defer author.Close()
			parentID := seedPinMutationMessage(t, socket, "#gateway-fixture", "live-stdio-parent-"+profile)
			request := core.Request{Version: 1, RequestID: "live-stdio-" + profile, Operation: core.EventsWatch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Limit: 8}
			p.write(t, liveFrame(t, request, credentials, profile))
			initial := p.read(t)
			if initial.Event != "page" || initial.Page == nil || len(initial.Page.Messages) != 1 || initial.Page.Cursor == "" || len(initial.Page.Events) != 0 {
				t.Fatalf("stdio live initial page: %+v", initial)
			}
			triggerLiveChat(t, author, irc.ChatRequest{Action: "cancel", Target: "#gateway-fixture", ID: parentID})
			readLiveFrameEvent(t, func() adapter.FrameResponse { return p.read(t) }, "live-live-stdio-"+profile, "cancel", initial.Page.Cursor)
			if err := p.command.Process.Signal(os.Interrupt); err != nil {
				t.Fatal(err)
			}
			if err := p.wait(); err != nil {
				t.Fatalf("stdio live shutdown: %v stderr=%s", err, p.stderr.String())
			}
		})
	}
}
