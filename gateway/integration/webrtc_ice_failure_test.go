//go:build darwin || linux

package gateway_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/pion/webrtc/v4"
)

func TestWebRTCInvalidTURNCredentialHasNoNativeIntake(t *testing.T) {
	server := newLocalTURN(t)
	credential := writeICEPassword(t, "incorrect TURN password")
	f := newWebRTCFixtureWith(t, "reliable_ordered", func(c *config.Config) {
		c.Listeners[0].WebRTC = &config.WebRTC{ICETransportPolicy: "relay", ICEServers: []config.WebRTCICE{{URLs: []string{"turn:" + server.address}, Username: server.username, CredentialFile: credential}}}
	})
	seedPinMutationMessage(t, f.config.Upstream.Address, "#gateway-fixture", "webrtc-turn-control")
	before := nativeWebTransportMessages(t, f.config.Upstream.Address, "#gateway-fixture")
	if len(before) != 1 {
		t.Fatalf("missing native control: %+v", before)
	}
	started := time.Now()
	pc, _, err := webRTCDataChannelWithICEAttempt(t, f, "reliable_ordered", webrtc.Configuration{ICETransportPolicy: webrtc.ICETransportPolicyRelay, ICEServers: []webrtc.ICEServer{{URLs: []string{"turn:" + server.address}, Username: server.username, Credential: server.password}}}, 7*time.Second)
	if pc != nil {
		_ = pc.Close()
	}
	if err == nil || time.Since(started) > 20*time.Second {
		t.Fatalf("invalid gateway TURN credential did not fail within bound: elapsed=%s err=%v", time.Since(started), err)
	}
	after := nativeWebTransportMessages(t, f.config.Upstream.Address, "#gateway-fixture")
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("invalid TURN changed native history: before=%+v after=%+v", before, after)
	}
}
