//go:build darwin || linux

package gateway_test

import (
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestWebRTCWatchInitialPageAndCancellation(t *testing.T) {
	for _, mode := range []string{"reliable_ordered", "reliable_unordered", "unreliable_ordered", "unreliable_unordered"} {
		t.Run(mode, func(t *testing.T) { testWebRTCWatchInitialPageAndCancellation(t, mode) })
	}
}

func testWebRTCWatchInitialPageAndCancellation(t *testing.T, mode string) {
	f := newWebRTCFixture(t, mode)
	parent := seedPinMutationMessage(t, f.config.Bindings[0].Address, "#gateway-fixture", "webrtc-watch-parent")
	session := newWebRTCSession(t, f, mode)
	request := core.Request{Version: 1, RequestID: "webrtc-watch", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8}
	response := webRTCSessionFrame(t, f, session, request, true, true)
	if response.Version != 1 || response.CorrelationID != "webrtc-"+request.RequestID || response.Event != "page" || response.Sequence != 1 || response.Page == nil || len(response.Page.Messages) == 0 || response.Page.Messages[0].ID != parent || response.Page.Cursor == "" {
		t.Fatalf("WebRTC watch initial response=%+v", response)
	}
	initialCursor := response.Page.Cursor
	author := dial(t, f.config.Bindings[0].Address, "webrtc-watch-author", "", false, true)
	defer author.Close()
	if err := author.SendWithID("#gateway-fixture", "WebRTC live watch", "webrtc-watch-live"); err != nil {
		t.Fatal(err)
	}
	observed := next(t, f.observer, func(e *irc.MessageEvent) bool {
		return e.Message == "WebRTC live watch" && e.Target == "#gateway-fixture" && e.ID != ""
	})
	live := webRTCReceiveFrame(t, session)
	if live.Event != "page" || live.Sequence != 2 || live.Page == nil || len(live.Page.Messages) == 0 || live.Page.Cursor == "" || live.Page.Cursor == initialCursor {
		t.Fatalf("WebRTC live watch=%+v", live)
	}
	message := live.Page.Messages[len(live.Page.Messages)-1]
	if message.ID != observed.ID || message.Message != observed.Message || message.From != observed.From || message.Target != observed.Target || message.Receipt != nil {
		t.Fatalf("WebRTC live metadata changed: page=%+v native=%+v", message, observed)
	}
	// A quiet lease emits a sequenced heartbeat without advancing its cursor.
	select {
	case <-session.responses:
		t.Fatal("WebRTC watch emitted an unexpected quiet-period frame")
	case <-time.After(250 * time.Millisecond):
	}
	heartbeat := webRTCReceiveFrame(t, session)
	if heartbeat.Event != "page" || heartbeat.Sequence != 3 || heartbeat.Page == nil || len(heartbeat.Page.Messages) != 0 || heartbeat.Page.Cursor == "" {
		t.Fatalf("WebRTC quiet heartbeat=%+v live=%+v", heartbeat, live)
	}
	_ = session.pc.Close()
	probe := newWebRTCSession(t, f, mode)
	capResponse := webRTCSessionFrame(t, f, probe, core.Request{Version: 1, RequestID: "webrtc-watch-after-cancel", Operation: core.CapabilitiesGet}, true, false)
	if capResponse.Event != "result" || capResponse.Result == nil || capResponse.Result.Capabilities == nil {
		t.Fatalf("WebRTC post-cancel capabilities=%+v", capResponse)
	}
	resumed := webRTCSessionFrame(t, f, probe, core.Request{Version: 1, RequestID: "webrtc-watch-resume", Operation: core.Watch, Target: request.Target, Cursor: heartbeat.Page.Cursor, Limit: 8}, true, false)
	if resumed.Event != "result" || resumed.Sequence != 1 || resumed.Result == nil || resumed.Result.Page == nil || resumed.Result.Page.Cursor == "" {
		t.Fatalf("WebRTC cursor resume=%+v", resumed)
	}
	for _, retained := range resumed.Result.Page.Messages {
		if retained.ID == observed.ID || retained.ID == parent {
			t.Fatalf("WebRTC resume replayed already delivered message: %+v", retained)
		}
	}
}
