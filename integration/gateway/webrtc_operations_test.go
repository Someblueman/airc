//go:build darwin || linux

package gateway_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestWebRTCNativeSendAndReadsAcrossDataChannelModes(t *testing.T) {
	for _, mode := range []string{"reliable_ordered", "reliable_unordered", "unreliable_ordered", "unreliable_unordered"} {
		t.Run(mode, func(t *testing.T) {
			f := newWebRTCFixture(t, mode)
			session := newWebRTCSession(t, f, mode)
			call := func(request core.Request, encrypted bool) adapter.FrameResponse {
				response := webRTCSessionFrame(t, f, session, request, encrypted, false)
				if response.Version != 1 || response.CorrelationID != "webrtc-"+request.RequestID || response.Event != "result" || response.Sequence != 1 || response.Result == nil || response.Result.Operation != request.Operation {
					t.Fatalf("WebRTC %s response=%+v failure=%#v", mode, response, response.Error)
				}
				return response
			}
			accepted := func(request core.Request, encrypted bool) core.Result {
				initial := call(request, encrypted)
				if initial.Result == nil || initial.Result.Result == nil || initial.Result.Result.RequestID != request.RequestID || !initial.Result.Result.Durable {
					t.Fatalf("WebRTC %s admission=%+v", mode, initial)
				}
				var last core.Result
				deadline := time.Now().Add(8 * time.Second)
				for attempt := 0; time.Now().Before(deadline); attempt++ {
					receipt := core.Request{Version: 1, RequestID: fmt.Sprintf("%s-receipt-%d", request.RequestID, attempt), Operation: core.ReceiptGet, LookupID: request.RequestID}
					result := call(receipt, encrypted)
					if result.Result == nil || result.Result.Result == nil {
						t.Fatalf("WebRTC %s receipt=%+v", mode, result)
					}
					last = *result.Result.Result
					if last.State == core.Accepted {
						return last
					}
					time.Sleep(40 * time.Millisecond)
				}
				t.Fatalf("WebRTC %s request did not reach accepted: %+v", mode, last)
				return core.Result{}
			}
			exerciseCarrierSendAndReads(t, call, accepted, nativeOperationFixture{config: f.config, observer: f.observer}, "webrtc-"+mode)
		})
	}
}
