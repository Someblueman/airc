package adapter

import (
	"encoding/json"
	"strings"
	"testing"
)

const validWebRTCOfferSDP = "v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\nm=application 9 UDP/DTLS/SCTP webrtc-datachannel\r\na=mid:0\r\n"

func TestWebRTCOfferValidation(t *testing.T) {
	for _, mode := range []string{"reliable_ordered", "reliable_unordered", "unreliable_ordered", "unreliable_unordered"} {
		body, err := json.Marshal(webrtcOffer{Version: 1, SDP: validWebRTCOfferSDP, Mode: mode})
		if err != nil {
			t.Fatal(err)
		}
		var got webrtcOffer
		if err := decodeWebRTCOffer(body, &got); err != nil || got.Mode != mode {
			t.Fatalf("mode %q rejected: %v", mode, err)
		}
	}
	for name, body := range map[string]string{
		"duplicate": `{"version":1,"sdp":"x","mode":"reliable_ordered","mode":"reliable_unordered"}`,
		"unknown":   `{"version":1,"sdp":"x","mode":"reliable_ordered","extra":1}`,
	} {
		t.Run(name, func(t *testing.T) {
			var offer webrtcOffer
			if err := decodeWebRTCOffer([]byte(body), &offer); err == nil {
				t.Fatal("accepted invalid offer")
			}
		})
	}
}

func TestWebRTCSdpDataOnlyBounds(t *testing.T) {
	if !validWebRTCSDP(strings.Replace(validWebRTCOfferSDP, "application 9 ", "application 50000 ", 1)) {
		t.Fatal("valid nontrickle application port rejected")
	}
	if !validWebRTCSDP(validWebRTCOfferSDP) {
		t.Fatal("valid data-only SDP rejected")
	}
	if validWebRTCSDP(strings.Replace(validWebRTCOfferSDP, "m=application", "m=audio", 1)) {
		t.Fatal("audio SDP accepted")
	}
	if validWebRTCSDP(validWebRTCOfferSDP + "m=application 9 UDP/DTLS/SCTP webrtc-datachannel\r\n") {
		t.Fatal("multiple data sections accepted")
	}
	if validWebRTCSDP(validWebRTCOfferSDP + strings.Repeat("a=candidate:long "+strings.Repeat("x", 2048)+"\r\n", 129)) {
		t.Fatal("oversized candidate SDP accepted")
	}
}

func TestWebRTCModeNames(t *testing.T) {
	if validWebRTCMode("") || validWebRTCMode("ordered") || !validWebRTCMode("reliable_ordered") {
		t.Fatal("mode allowlist incorrect")
	}
}
