//go:build darwin || linux

package gateway_test

import (
	"net"
	"os"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestWebRTCActiveWatchJoinsOnSIGINTAndReleasesPort(t *testing.T) {
	f := newWebRTCFixture(t, "reliable_ordered")
	statusClient := nativeStatusClient(t, f.config.Bindings[0].Address)
	baseline := nativeObserverCount(t, statusClient)
	session := newWebRTCSession(t, f, "reliable_ordered")
	watch := core.Request{Version: 1, RequestID: "webrtc-lifecycle-shutdown", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8}
	page := webRTCSessionFrame(t, f, session, watch, true, true)
	if page.Event != "page" || page.Page == nil {
		t.Fatalf("watch page=%+v", page)
	}
	waitNativeObserverCount(t, statusClient, baseline+1)
	if err := f.process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.process.done:
		if f.process.err != nil {
			t.Fatalf("WebRTC SIGINT: %v stderr=%s", f.process.err, f.process.stderr.String())
		}
	case <-time.After(4 * time.Second):
		t.Fatal("WebRTC active watch did not join on SIGINT")
	}
	listener, err := net.Listen("tcp", f.address)
	if err != nil {
		t.Fatalf("WebRTC listener retained after SIGINT: %v", err)
	}
	_ = listener.Close()
	waitNativeObserverCountAtMost(t, statusClient, baseline)
}

func TestWebRTCPeerCloseReleasesNativeWatchAndAdmission(t *testing.T) {
	f := newWebRTCFixtureWith(t, "reliable_ordered", func(c *config.Config) {
		c.Limits.Sessions = 8
		c.Limits.SessionsPerPrincipal = 2
		c.Limits.Watches = 4
		c.Limits.WatchesPerPrincipal = 1
	})
	statusClient := nativeStatusClient(t, f.config.Bindings[0].Address)
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
	f := newWebRTCFixture(t, "reliable_ordered")
	_, channel := webRTCDataChannel(t, f, "reliable_ordered")
	closed := make(chan struct{})
	channel.OnClose(func() { close(closed) })
	deadline := time.Now().Add(31 * time.Second)
	for time.Now().Before(deadline) {
		if err := channel.Send([]byte{0}); err != nil {
			break
		}
		time.Sleep(time.Second)
	}
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("invalid traffic kept WebRTC peer alive past idle deadline")
	}
}
