package config

import "testing"

func TestWebRTCURIUsesICEGrammar(t *testing.T) {
	for _, uri := range []string{"stun:localhost", "stun:[::1]", "turn:example.test:3478?transport=tcp", "turns:[::1]:5349"} {
		if _, err := validateWebRTCURL(uri); err != nil {
			t.Fatalf("valid ICE URI rejected: %s: %v", uri, err)
		}
	}
	for _, uri := range []string{"stun://localhost/path", "turn://localhost", "stun:localhost?transport=udp", "turn:localhost:0", "turn:localhost:65536", "turn:localhost?transport=udp&transport=tcp", "stun:localhost#fragment"} {
		if _, err := validateWebRTCURL(uri); err == nil {
			t.Fatalf("invalid ICE URI accepted: %s", uri)
		}
	}
}
