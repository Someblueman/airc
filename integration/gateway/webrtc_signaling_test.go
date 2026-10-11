//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/pion/webrtc/v4"
)

func TestWebRTCSignalingAuthOriginAndPeerQuota(t *testing.T) {
	f := newWebRTCFixtureWith(t, "reliable_ordered", func(c *config.Config) {
		c.Limits.Workers = 4
		c.Limits.Sessions = 4
		c.Limits.SessionsPerPrincipal = 2
		c.Limits.Watches = 2
		c.Limits.WatchesPerPrincipal = 1
		c.Listeners[0].AllowedOrigins = []string{"https://allowed.example"}
	})
	transport := &http.Transport{TLSClientConfig: f.cert.Client.Clone()}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport}
	post := func(origin, body string) int {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+f.address+"/v1/webrtc/offer", bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+f.token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		_, _ = io.ReadAll(io.LimitReader(res.Body, 4097))
		return res.StatusCode
	}
	if got := post("https://wrong.example", `{"version":1,"sdp":"bad","mode":"reliable_ordered"}`); got != http.StatusForbidden {
		t.Fatalf("wrong origin status=%d", got)
	}
	if got := post("https://allowed.example", `{"version":1,"sdp":"bad","mode":"reliable_ordered","mode":"reliable_ordered"}`); got != http.StatusBadRequest {
		t.Fatalf("duplicate offer status=%d", got)
	}
	first := newWebRTCSession(t, f, "reliable_ordered")
	second, _, err := webRTCDataChannelWithICEAttempt(t, f, "reliable_ordered", webrtc.Configuration{}, 5*time.Second)
	if err != nil {
		t.Fatal("second peer did not consume the available shared slot", err)
	}
	t.Cleanup(func() { _ = second.Close() })
	_, _, err = webRTCDataChannelWithICEAttempt(t, f, "reliable_ordered", webrtc.Configuration{}, 2*time.Second)
	if err == nil {
		t.Fatal("third peer bypassed global/per-principal quota")
	}
	_ = first.pc.Close()
	if got := post("https://allowed.example", `{"version":1,"sdp":"bad","mode":"reliable_ordered"}`); got != http.StatusBadRequest {
		t.Fatalf("invalid offer after active peers status=%d", got)
	}
}

func TestWebRTCPrincipalBindingAndDurableRevocation(t *testing.T) {
	var otherMaterial webRTCOtherPrincipal
	f := newWebRTCFixtureWith(t, "reliable_ordered", func(c *config.Config) {
		otherMaterial = addWebRTCOtherPrincipal(t, c)
	})
	otherCredentials := webRTCOtherCredentials(t, f, otherMaterial)
	other := f
	other.token, _ = auth.ReadToken(f.config.Principals[1].BearerTokenFile)

	before := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture")
	session := newWebRTCSession(t, other, "reliable_ordered")
	closed := make(chan struct{})
	session.channel.OnClose(func() { close(closed) })
	attack := core.Request{Version: 1, RequestID: "webrtc-cross-principal", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "must-not-post-cross-principal"}
	sendWebRTCFrame(t, session, f.credentials, attack, true)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("peer accepted a detached envelope from a different principal")
	}
	if after := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture"); len(after) != len(before) || !reflect.DeepEqual(after, before) {
		t.Fatalf("cross-principal frame changed native history: before=%+v after=%+v", before, after)
	}
	ownedCross := newWebRTCSession(t, f, "reliable_ordered")
	closedOwned := make(chan struct{})
	ownedCross.channel.OnClose(func() { close(closedOwned) })
	sendWebRTCFrame(t, ownedCross, otherCredentials, attack, true)
	select {
	case <-closedOwned:
	case <-time.After(5 * time.Second):
		t.Fatal("peer accepted a detached envelope signed by a different principal")
	}
	rpcNoJobs(t, f.config.StateDir)

	owned := newWebRTCSession(t, f, "reliable_ordered")
	revokeWebRTCPrincipal(t, f.config.StateDir, "device")
	revoked := webRTCSessionFrame(t, f, owned, core.Request{Version: 1, RequestID: "webrtc-revoked", Operation: core.CapabilitiesGet}, true, false)
	if revoked.Error == nil || revoked.Error.Code != core.Forbidden {
		t.Fatalf("revoked peer request was not denied: %+v", revoked)
	}
	if _, _, err := webRTCDataChannelWithICEAttempt(t, f, "reliable_ordered", webrtc.Configuration{}, 3*time.Second); err == nil {
		t.Fatal("revoked principal opened a new WebRTC peer")
	}
	rpcNoJobs(t, f.config.StateDir)
}
