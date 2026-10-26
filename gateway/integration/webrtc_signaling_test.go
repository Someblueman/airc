//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/pion/webrtc/v4"
)

func TestWebRTCSignalingOriginAndPeerQuota(t *testing.T) {
	f := newWebRTCFixtureWith(t, "reliable_ordered", func(c *config.Config) {
		c.Limits.Workers = 4
		c.Limits.Sessions = 4
		c.Limits.SessionsPerAuthor = 2
		c.Limits.Watches = 2
		c.Limits.WatchesPerAuthor = 1
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
		setAuthorHeaders(req, f.token)
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
		t.Fatal("third peer bypassed global/per-source quota")
	}
	_ = first.pc.Close()
	if got := post("https://allowed.example", `{"version":1,"sdp":"bad","mode":"reliable_ordered"}`); got != http.StatusBadRequest {
		t.Fatalf("invalid offer after active peers status=%d", got)
	}
}
