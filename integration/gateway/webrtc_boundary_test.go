//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"io"
	"net/http"
	"testing"
)

func TestWebRTCOfferValidationDoesNotAdmitPeers(t *testing.T) {
	f := newWebRTCFixture(t, "reliable_ordered")
	transport := &http.Transport{TLSClientConfig: f.cert.Client.Clone()}
	t.Cleanup(transport.CloseIdleConnections)
	call := func(method, token, body string) int {
		t.Helper()
		req, err := http.NewRequest(method, "https://"+f.address+"/v1/webrtc/offer", bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			setAuthorHeaders(req, token)
		}
		if method == http.MethodPost {
			req.Header.Set("Content-Type", "application/json")
		}
		res, err := (&http.Client{Transport: transport}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		_, _ = io.ReadAll(io.LimitReader(res.Body, 4097))
		return res.StatusCode
	}
	if got := call(http.MethodGet, "", ""); got != http.StatusBadRequest {
		t.Fatalf("GET status=%d", got)
	}
	// Without a authorSpec token the offer opens an author peer; its SDP is
	// still validated before any peer is admitted.
	if got := call(http.MethodPost, "", `{"version":1,"sdp":"x","mode":"reliable_ordered"}`); got != http.StatusBadRequest {
		t.Fatalf("author offer with invalid SDP status=%d", got)
	}
	if got := call(http.MethodPost, f.token, `{"version":1,"sdp":"x","mode":"reliable_ordered"}`); got != http.StatusBadRequest {
		t.Fatalf("invalid SDP status=%d", got)
	}
	if got := call(http.MethodPost, f.token, `{"version":1,"sdp":"v=0","mode":"invalid"}`); got != http.StatusBadRequest {
		t.Fatalf("invalid mode status=%d", got)
	}
}
