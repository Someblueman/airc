//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/adapter"
	"github.com/Someblueman/airc/gateway/internal/core"
)

func readLiveFrameEvent(t *testing.T, read func() adapter.FrameResponse, correlation, action, cursor string) {
	t.Helper()
	for i := 0; i < 4; i++ {
		response := read()
		if response.Version != 1 || response.CorrelationID != correlation || response.Event != "page" || response.Page == nil {
			t.Fatalf("invalid live stream frame: %+v", response)
		}
		page := liveWirePage{Cursor: response.Page.Cursor, Status: response.Page.Status}
		for _, event := range response.Page.Events {
			page.Events = append(page.Events, mustJSON(t, event))
		}
		if len(page.Events) == 0 {
			continue
		}
		nextLiveEvent(t, page, action)
		if response.Page.Cursor != cursor {
			t.Fatalf("live stream event changed cursor: got=%q want=%q", response.Page.Cursor, cursor)
		}
		return
	}
	t.Fatalf("live stream emitted no %q event", action)
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestEventsWatchFramedAndWebSocketStreams(t *testing.T) {
	t.Parallel()
	t.Run("framed", func(t *testing.T) {
		f := newReactionTransportFixture(t)
		defer f.framed.Close()
		author := dial(t, f.socket, "live-framed-author", "", false, true)
		defer author.Close()
		request := core.Request{Version: 1, RequestID: "live-framed", Operation: core.EventsWatch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Limit: 8}
		writeFrame(t, f.framed, liveFrame(t, request, f.cred, "encrypted"))
		initial := readFrame(t, f.framed)
		if initial.Event != "page" || initial.Page == nil || initial.Page.Cursor != "" || len(initial.Page.Events) != 0 {
			t.Fatalf("framed live initial page: %+v", initial)
		}
		triggerLiveSignal(t, author, "typing")
		readLiveFrameEvent(t, func() adapter.FrameResponse { return readFrame(t, f.framed) }, "live-live-framed", "typing", "")
	})

	t.Run("framed-tls", func(t *testing.T) {
		conn, credentials, stop, socket := framedTLSCardFixture(t)
		defer conn.Close()
		defer stop()
		author := dial(t, socket, "live-tls-author", "", false, true)
		defer author.Close()
		request := core.Request{Version: 1, RequestID: "live-framed-tls", Operation: core.EventsWatch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Limit: 8}
		frame := liveFrame(t, request, credentials, "signed")
		writeFrame(t, conn, frame)
		initial := readFrame(t, conn)
		if initial.Event != "page" || initial.Page == nil || initial.Page.Cursor != "" || len(initial.Page.Events) != 0 {
			t.Fatalf("TLS framed live initial page: %+v", initial)
		}
		trigger := dial(t, socket, "live-tls-trigger", "", false, true)
		defer trigger.Close()
		triggerLiveSignal(t, trigger, "thinking")
		readLiveFrameEvent(t, func() adapter.FrameResponse { return readFrame(t, conn) }, "live-live-framed-tls", "thinking", "")
	})

	t.Run("wss", func(t *testing.T) {
		f := websocketGatewayFixture(t, true, "127.0.0.1:0", nil)
		defer f.stop()
		author := dial(t, f.socket, "live-wss-author", "", false, true)
		defer author.Close()
		request := core.Request{Version: 1, RequestID: "live-wss", Operation: core.EventsWatch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Limit: 8}
		websocketWrite(t, f.conn, liveFrame(t, request, f.credentials, "signed"))
		initial := websocketRead(t, f.conn)
		if initial.Event != "page" || initial.Page == nil || initial.Page.Cursor != "" || len(initial.Page.Events) != 0 {
			t.Fatalf("WSS live initial page: %+v", initial)
		}
		triggerLiveSignal(t, author, "thinking")
		readLiveFrameEvent(t, func() adapter.FrameResponse { return websocketRead(t, f.conn) }, "live-live-wss", "thinking", "")
	})
}
