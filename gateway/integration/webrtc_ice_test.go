//go:build darwin || linux

package gateway_test

import (
	"context"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
	"github.com/pion/webrtc/v4"
)

// TestWebRTCRelayICE proves the configured relay path is actually selected.
// It deliberately uses a loopback TURN server: this verifies gateway and
// credential wiring, not traversal through a remote NAT.
func TestWebRTCRelayICE(t *testing.T) {
	turnFixture := newLocalTURN(t)
	credential := writeICEPassword(t, turnFixture.password)
	f := newWebRTCFixtureWith(t, "reliable_ordered", func(c *config.Config) {
		c.Listeners[0].WebRTC = &config.WebRTC{
			ICETransportPolicy: "relay",
			ICEServers: []config.WebRTCICE{{
				URLs:           []string{"turn:" + turnFixture.address},
				Username:       turnFixture.username,
				CredentialFile: credential,
			}},
		}
	})
	servers := []webrtc.ICEServer{{URLs: []string{"turn:" + turnFixture.address}, Username: turnFixture.username, Credential: turnFixture.password}}
	session := newWebRTCSessionWithICE(t, f, "reliable_ordered", webrtc.Configuration{ICEServers: servers, ICETransportPolicy: webrtc.ICETransportPolicyRelay})
	defer session.pc.Close()
	if pair, err := session.pc.SCTP().Transport().ICETransport().GetSelectedCandidatePair(); err != nil || pair == nil || pair.Local == nil || pair.Local.Typ != webrtc.ICECandidateTypeRelay || pair.Remote == nil || pair.Remote.Typ != webrtc.ICECandidateTypeRelay {
		t.Fatalf("selected pair is not relay: pair=%v err=%v", pair, err)
	}
	if session.channel == nil {
		t.Fatal("missing opened data channel")
	}
	request := core.Request{Version: 1, RequestID: "webrtc-relay-send", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "WebRTC relay topology"}
	response := webRTCSessionFrame(t, f, session, request, true, false)
	if response.Result == nil || response.Result.Result == nil || !response.Result.Result.Durable {
		t.Fatalf("relay send was not durably admitted: %+v", response)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	attempt := 0
	acceptedResult := acceptedReceipt(t, 8*time.Second, 50*time.Millisecond, "the relay send to reach accepted", func() *core.Result {
		receipt := core.Request{Version: 1, RequestID: request.RequestID + "-receipt-" + string(rune('a'+attempt%26)), Operation: core.ReceiptGet, LookupID: request.RequestID}
		attempt++
		r := webRTCSessionFrame(t, f, session, receipt, true, false)
		if r.Result == nil {
			return nil
		}
		return r.Result.Result
	})
	if acceptedResult.MessageID == "" || acceptedResult.SendAttempts != 1 {
		t.Fatalf("relay accepted result lacks durable native identity: %+v", acceptedResult)
	}
	history := core.Request{Version: 1, RequestID: request.RequestID + "-history", Operation: core.HistoryRead, Target: request.Target, Start: "retained", Limit: 8}
	h := webRTCSessionFrame(t, f, session, history, true, false)
	if h.Event != "result" || h.Result == nil || h.Result.Page == nil || len(h.Result.Page.Messages) == 0 {
		t.Fatalf("relay history response=%+v", h)
	}
	var found bool
	for _, message := range h.Result.Page.Messages {
		if message.ID == acceptedResult.MessageID {
			found = message.Message == request.Message && message.From == "fixture-author" && message.Target == request.Target.Name
		}
	}
	if !found {
		t.Fatalf("relay history omitted accepted native message id=%q: %+v", acceptedResult.MessageID, h.Result.Page.Messages)
	}
	assertNativeReadFacts(t, nativeOperationFixture{config: f.config, observer: f.observer}, history, h.Result)
	watch := newWebRTCSessionWithICE(t, f, "reliable_ordered", webrtc.Configuration{ICEServers: servers, ICETransportPolicy: webrtc.ICETransportPolicyRelay})
	defer watch.pc.Close()
	w := webRTCSessionFrame(t, f, watch, core.Request{Version: 1, RequestID: request.RequestID + "-watch", Operation: core.Watch, Target: request.Target, Start: "retained", Limit: 8}, true, true)
	if w.Event != "page" || w.Page == nil {
		t.Fatalf("relay watch response=%+v", w)
	}
	for {
		select {
		case event := <-f.observer.Events():
			message, ok := event.(*irc.MessageEvent)
			if ok && message.Message == request.Message && message.Target == request.Target.Name && message.ID != "" {
				return
			}
		case <-ctx.Done():
			t.Fatal("native observer did not see relay send")
		}
	}
}

func TestWebRTCConfiguredSTUNICE(t *testing.T) {
	stunFixture := newLocalTURN(t)
	f := newWebRTCFixtureWith(t, "reliable_ordered", func(c *config.Config) {
		c.Listeners[0].WebRTC = &config.WebRTC{ICEServers: []config.WebRTCICE{{URLs: []string{"stun:" + stunFixture.address}}}}
	})
	servers := []webrtc.ICEServer{{URLs: []string{"stun:" + stunFixture.address}}}
	session := newWebRTCSessionWithICE(t, f, "reliable_ordered", webrtc.Configuration{ICEServers: servers})
	defer session.pc.Close()
	if stunFixture.requests.Load() == 0 || stunFixture.responses.Load() == 0 {
		t.Fatalf("configured STUN server binding traffic requests=%d responses=%d", stunFixture.requests.Load(), stunFixture.responses.Load())
	}
	pair, err := session.pc.SCTP().Transport().ICETransport().GetSelectedCandidatePair()
	if err != nil || pair == nil || pair.Local == nil {
		t.Fatalf("missing selected STUN pair: pair=%v err=%v", pair, err)
	}
	if pair.Local.Typ != webrtc.ICECandidateTypeHost && pair.Local.Typ != webrtc.ICECandidateTypeSrflx {
		t.Fatalf("unexpected selected STUN candidate type: %v", pair.Local.Typ)
	}
}
