//go:build darwin || linux

package gateway_test

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestWebRTCNativeSendAndReadsAcrossDataChannelModes(t *testing.T) {
	for _, mode := range []string{"reliable_ordered", "reliable_unordered", "unreliable_ordered", "unreliable_unordered"} {
		t.Run(mode, func(t *testing.T) { exerciseWebRTCNative(t, mode, false) })
	}
}

func TestWebRTCReliableOrderedAuthenticatedSendAndBoundedResume(t *testing.T) {
	exerciseWebRTCNative(t, "reliable_ordered", true)
}

func TestWebRTCReliableUnorderedAuthenticatedSendAndBoundedResume(t *testing.T) {
	exerciseWebRTCNative(t, "reliable_unordered", true)
}

func TestWebRTCUnreliableOrderedAuthenticatedSendAndBoundedResume(t *testing.T) {
	exerciseWebRTCNative(t, "unreliable_ordered", true)
}

func exerciseWebRTCNative(t *testing.T, mode string, bounded bool) {
	t.Helper()
	f := newWebRTCFixture(t, mode)
	session := newWebRTCSession(t, f, mode)
	call := func(request core.Request, encrypted bool) adapter.FrameResponse {
		response := webRTCSessionFrame(t, f, session, request, encrypted, false)
		if response.Version != 1 || response.CorrelationID != "webrtc-"+request.RequestID || response.Event != "result" || response.Sequence != 1 || response.Error != nil || response.Result == nil || response.Result.Operation != request.Operation {
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
	if bounded {
		qualifyWebRTCBoundedReads(t, f, session, mode, call, accepted)
		return
	}
	exerciseCarrierSendAndReads(t, call, accepted, nativeOperationFixture{config: f.config, observer: f.observer}, "webrtc-"+mode)
}

func qualifyWebRTCBoundedReads(t *testing.T, f webRTCFixture, session *webRTCSession, mode string, call nativeOperationCall, accepted nativeAcceptedCall) {
	t.Helper()
	ordered := mode == "reliable_ordered" || mode == "unreliable_ordered"
	unreliable := mode == "unreliable_ordered" || mode == "unreliable_unordered"
	retransmits := session.channel.MaxRetransmits()
	if session.channel.Ordered() != ordered || session.channel.MaxPacketLifeTime() != nil || (!unreliable && retransmits != nil) || (unreliable && (retransmits == nil || *retransmits != 0)) {
		t.Fatal("channel negotiation differs", session.channel.Ordered(), session.channel.MaxRetransmits(), session.channel.MaxPacketLifeTime())
	}
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	for i := 0; i < 2; i++ {
		request := core.Request{Version: 1, RequestID: fmt.Sprintf("%s-bounded-send-%d", mode, i), Operation: core.Send, Target: room, Message: fmt.Sprintf("WebRTC %s message %d\nwhole Unicode body 🙂", mode, i)}
		result := accepted(request, true)
		if result.SendAttempts != 1 || result.MessageID == "" || result.RequestID != request.RequestID {
			t.Fatal("WebRTC native acceptance mismatch", result)
		}
		records := nativeWebTransportMessages(t, f.config.Bindings[0].Address, room.Name)
		if len(records) != i+1 || records[i].ID != result.MessageID || records[i].Message != request.Message || records[i].From != "fixture-author" || records[i].Target != room.Name {
			t.Fatal("WebRTC native record mismatch", records, result)
		}
	}
	native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, room.Name)
	request := core.Request{Version: 1, RequestID: mode + "-bounded", Operation: core.HistoryRead, Target: room, Start: "retained", Limit: 1}
	for i, record := range native {
		page := call(request, true).Result.Page
		status := "more"
		if i == len(native)-1 {
			status = "ok"
		}
		if page == nil || len(page.Messages) != 1 || !reflect.DeepEqual(page.Messages[0], record) || page.Status != status || page.Cursor == "" || page.Gap {
			t.Fatal("WebRTC bounded read differs from native CHECK", page)
		}
		request.Start, request.Cursor = "", page.Cursor
	}
	page := call(request, true).Result.Page
	if page == nil || len(page.Messages) != 0 || page.Status != "ok" || page.Cursor == "" || page.Gap {
		t.Fatal("WebRTC caught-up resume mismatch", page)
	}
}
