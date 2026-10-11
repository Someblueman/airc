//go:build darwin || linux

package gateway_test

import (
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestWebRTCPeerCloseReleasesNativeWatchAndAdmission(t *testing.T) {
	f := newWebRTCFixtureWith(t, "reliable_ordered", func(c *config.Config) {
		c.Limits.Sessions = 8
		c.Limits.SessionsPerAuthor = 2
		c.Limits.Watches = 4
		c.Limits.WatchesPerAuthor = 1
	})
	statusClient := nativeStatusClient(t, f.config.Upstream.Address)
	baseline := nativeObserverCount(t, statusClient)
	first := newWebRTCSession(t, f, "reliable_ordered")
	watch := core.Request{Version: 1, RequestID: "webrtc-peer-close-first", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8}
	if response := webRTCSessionFrame(t, f, first, watch, true, true); response.Event != "page" || response.Page == nil {
		t.Fatalf("first watch=%+v", response)
	}
	waitNativeObserverCount(t, statusClient, baseline+1)
	if err := first.pc.Close(); err != nil {
		t.Fatal(err)
	}
	waitNativeObserverCountAtMost(t, statusClient, baseline)
	second := newWebRTCSession(t, f, "reliable_ordered")
	request := core.Request{Version: 1, RequestID: "webrtc-peer-close-second", Operation: core.CapabilitiesGet}
	response := webRTCSessionFrame(t, f, second, request, true, false)
	if response.Event != "result" || response.Result == nil || response.Result.Capabilities == nil {
		t.Fatalf("second peer admission=%+v", response)
	}
}

func TestWebRTCInvalidTrafficDoesNotRenewPeerIdleDeadline(t *testing.T) {
	idle := shortGatewayTimer(t, webRTCPeerIdleTimer)
	f := newWebRTCFixture(t, "reliable_ordered")
	valid := newWebRTCSession(t, f, "reliable_ordered")
	initial := webRTCSessionFrame(t, f, valid, core.Request{Version: 1, RequestID: "webrtc-idle-initial-activity", Operation: core.CapabilitiesGet}, true, false)
	if initial.Result == nil || initial.Result.Capabilities == nil {
		t.Fatal("initial authenticated WebRTC activity failed", initial)
	}
	validStarted := time.Now()
	started := time.Now()
	_, channel := webRTCDataChannel(t, f, "reliable_ordered")
	closed := make(chan struct{})
	channel.OnClose(func() { close(closed) })
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	renew := time.NewTimer(time.Until(validStarted.Add(idle * 2 / 3)))
	defer renew.Stop()
	deadline := time.NewTimer(idle + 6*time.Second)
	defer deadline.Stop()
	renewed := false
	var sendErr error
	for {
		select {
		case <-closed:
			if !renewed || time.Since(started) < idle-2*time.Second {
				t.Fatal("WebRTC idle witness closed before its renewal control or idle bound", time.Since(started))
			}
			// Cross the valid peer's original idle deadline plus one reaper tick.
			// Its later usability must depend on authenticated renewal.
			if remaining := time.Until(validStarted.Add(idle + time.Second)); remaining > 0 {
				time.Sleep(remaining)
			}
			response := webRTCSessionFrame(t, f, valid, core.Request{Version: 1, RequestID: "webrtc-idle-after-original-deadline", Operation: core.CapabilitiesGet}, true, false)
			if response.Result == nil || response.Result.Capabilities == nil {
				t.Fatal("authenticated WebRTC renewal did not retain usable peer", response)
			}
			return
		case <-ticker.C:
			sendErr = channel.Send([]byte{0})
		case <-renew.C:
			response := webRTCSessionFrame(t, f, valid, core.Request{Version: 1, RequestID: "webrtc-idle-valid-renewal", Operation: core.CapabilitiesGet}, true, false)
			if response.Result == nil || response.Result.Capabilities == nil {
				t.Fatal("authenticated WebRTC renewal failed", response)
			}
			renewed = true
		case <-deadline.C:
			t.Fatal("invalid traffic kept WebRTC peer alive past idle deadline", sendErr)
		}
	}
}
