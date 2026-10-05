//go:build darwin || linux

package gateway_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/pion/webrtc/v4"
)

func TestWebRTCSlowReaderWriteBoundReleasesNativeWatch(t *testing.T) {
	f := newWebRTCFixture(t, "reliable_ordered")
	author := dial(t, f.config.Bindings[0].Address, "webrtc-slow-author", "", false, true)
	t.Cleanup(func() { _ = author.Close() })
	baseline := nativeObserverCount(t, f.config.Bindings[0].Address)
	session := newWebRTCSession(t, f, "reliable_ordered")
	watch := core.Request{Version: 1, RequestID: "webrtc-slow-reader", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8}
	page := webRTCSessionFrame(t, f, session, watch, true, true)
	if page.Event != "page" || page.Page == nil {
		t.Fatalf("initial watch=%+v", page)
	}
	waitNativeObserverCount(t, f.config.Bindings[0].Address, baseline+1)
	// Stop consuming DataChannel messages. The gateway must hit its synchronous
	// 64 KiB/3 second write bound and release the native watch lease.
	blocked := make(chan struct{})
	var unblockOnce sync.Once
	unblock := func() { unblockOnce.Do(func() { close(blocked) }) }
	t.Cleanup(unblock)
	entered := make(chan struct{})
	closed := make(chan struct{})
	var enteredOnce sync.Once
	session.channel.OnMessage(func(webrtc.DataChannelMessage) {
		enteredOnce.Do(func() { close(entered) })
		<-blocked
	})
	session.channel.OnClose(func() { close(closed) })
	started := time.Now()
	for i := 0; i < 80; i++ {
		if err := author.SendWithID("#gateway-fixture", fmt.Sprintf("webrtc-slow-%d %s", i, strings.Repeat("x", 3000)), fmt.Sprintf("webrtc-slow-%d", i)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(30 * time.Millisecond)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("slow-reader callback was never entered")
	}
	waitNativeObserverCountAtMost(t, f.config.Bindings[0].Address, baseline)
	unblock()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("slow WebRTC peer remained open after bounded writer cleanup")
	}
	if elapsed := time.Since(started); elapsed > 8*time.Second {
		t.Fatalf("slow-reader cleanup elapsed=%s", elapsed)
	}
	// A fresh peer remains admissible after the bounded writer closes the slow one.
	fresh := newWebRTCSession(t, f, "reliable_ordered")
	response := webRTCSessionFrame(t, f, fresh, core.Request{Version: 1, RequestID: "webrtc-slow-reader-recovery", Operation: core.CapabilitiesGet}, true, false)
	if response.Event != "result" || response.Result == nil || response.Result.Capabilities == nil {
		t.Fatalf("fresh peer after slow-reader cleanup=%+v", response)
	}
}
