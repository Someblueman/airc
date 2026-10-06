//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
	"github.com/pion/webrtc/v4"
)

func TestWebRTCUnreliableDroppedResponseRetryKeepsOneDurableIntake(t *testing.T) {
	webRTCUnreliableDroppedResponseRetry(t, "unreliable_unordered")
}

func TestWebRTCUnreliableOrderedDroppedResponseRetryKeepsOneDurableIntake(t *testing.T) {
	webRTCUnreliableDroppedResponseRetry(t, "unreliable_ordered")
}

func webRTCUnreliableDroppedResponseRetry(t *testing.T, mode string) {
	t.Helper()
	f := newWebRTCFixture(t, mode)
	issued, expires := time.Now().Unix(), time.Now().Unix()+120
	request := core.Request{Version: 1, RequestID: "webrtc-dropped-response", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "WebRTC dropped response retry", IssuedAt: &issued, ExpiresAt: &expires}
	envelope := f.credentials.token(t, request, true)
	first := newWebRTCSession(t, f, mode)
	var dropped atomic.Bool
	droppedObserved := make(chan struct{})
	first.channel.OnMessage(func(webrtc.DataChannelMessage) {
		if dropped.CompareAndSwap(false, true) {
			close(droppedObserved)
		}
	})
	body, err := json.Marshal(adapter.FrameRequest{Version: 1, CorrelationID: "webrtc-first-attempt", Profile: "encrypted", Envelope: envelope})
	if err != nil {
		t.Fatal(err)
	}
	var id [16]byte
	id[0] = 1
	webRTCSessionSendBody(t, first, body, id)
	select {
	case <-droppedObserved:
	case <-time.After(5 * time.Second):
		t.Fatal("unreliable WebRTC response was not observed for deliberate drop")
	}
	_ = first.pc.Close()

	native := next(t, f.observer, func(event *irc.MessageEvent) bool {
		return event.Message == request.Message && event.Target == request.Target.Name
	})
	if native.ID == "" {
		t.Fatalf("native acceptance had no durable message ID: %+v", native)
	}

	retry := newWebRTCSession(t, f, mode)
	replayed := webRTCSessionFrame(t, f, retry, request, true, false)
	if replayed.Error != nil {
		t.Fatalf("WebRTC original retry error=%+v native=%+v", replayed.Error, native)
	}
	if replayed.Result == nil || replayed.Result.Result == nil || replayed.Result.Result.State != core.Accepted || replayed.Result.Result.MessageID != native.ID || replayed.Result.Result.SendAttempts != 1 {
		t.Fatalf("WebRTC original retry result=%+v native=%+v", replayed, native)
	}
	deadline := time.Now().Add(8 * time.Second)
	accepted := *replayed.Result.Result
	for attempt := 0; time.Now().Before(deadline); attempt++ {
		receipt := core.Request{Version: 1, RequestID: "webrtc-dropped-receipt-" + string(rune('a'+attempt)), Operation: core.ReceiptGet, LookupID: request.RequestID}
		response := webRTCSessionFrame(t, f, retry, receipt, true, false)
		if response.Result != nil && response.Result.Result != nil {
			accepted = *response.Result.Result
			if accepted.State == core.Accepted {
				break
			}
		}
		time.Sleep(40 * time.Millisecond)
	}
	if accepted.State != core.Accepted || accepted.MessageID == "" || accepted.SendAttempts != 1 {
		t.Fatalf("WebRTC dropped-response replay result=%+v", accepted)
	}
	if accepted.MessageID != native.ID {
		t.Fatalf("WebRTC receipt changed native ID: accepted=%+v native=%+v", accepted, native)
	}
	records := nativeWebTransportMessages(t, f.config.Bindings[0].Address, request.Target.Name)
	if len(records) != 1 || records[0].ID != native.ID || records[0].From != native.From || records[0].Target != request.Target.Name || records[0].Message != request.Message {
		t.Fatalf("WebRTC retry native intake records=%+v native=%+v", records, native)
	}
}
