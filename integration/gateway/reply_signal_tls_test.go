//go:build darwin || linux

package gateway_test

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestReplySignalOverFramedTLS(t *testing.T) {
	conn, credentials, stop, socket := framedTLSCardFixture(t)
	defer conn.Close()
	defer stop()
	target := core.Target{Kind: "room", Name: "#gateway-fixture"}
	parent := seedPinMutationMessage(t, socket, target.Name, "reply-signal-tls-parent")
	observer := newReplySignalObserver(t, socket, target)
	call := func(request core.Request, correlation string) adapter.FrameResponse {
		writeFrame(t, conn, frameRequest{Version: 1, CorrelationID: correlation, Profile: "plain", Request: credentials.token(t, request, true)})
		response := readFrame(t, conn)
		if response.CorrelationID != correlation || response.Event != "result" {
			t.Fatalf("TLS reply signal response mismatch: %+v", response)
		}
		return response
	}
	set := replySignalRequest("reply-signal-tls-set", parent, core.ReplySignalSet, &target)
	set.ReplySignal = &core.ReplySignalInput{Note: "TLS working", TTLSeconds: 5}
	if response := call(set, set.RequestID); response.Error != nil || response.Result == nil {
		t.Fatalf("TLS reply signal set admission: %+v", response)
	}
	assertReplySignalFacts(t, waitReplySignalResult(t, call, set), set, nextReplySignal(t, observer, parent))
	cancel := replySignalRequest("reply-signal-tls-cancel", parent, core.ReplySignalCancel, &target)
	if response := call(cancel, cancel.RequestID); response.Error != nil || response.Result == nil {
		t.Fatalf("TLS reply signal cancel admission: %+v", response)
	}
	assertReplySignalFacts(t, waitReplySignalResult(t, call, cancel), cancel, nextReplySignal(t, observer, parent))
}
