package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/core"
)

func TestWebRTCListenerValidationAndOptions(t *testing.T) {
	base := strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"rtc","protocol":"webrtc","enabled":true,"tls_cert_file":"/private/cert.pem","tls_key_file":"/private/key.pem","operations":["send"],"webrtc":{"ice_servers":[{"urls":["stun:stun.example.com:3478"]}]}}]`, 1)
	c, err := Parse([]byte(base))
	if err != nil || c.Listeners[0].Protocol != "webrtc" {
		t.Fatalf("valid WebRTC config=%+v err=%v", c, err)
	}
	for _, bad := range []string{
		strings.Replace(base, `,"tls_cert_file":"/private/cert.pem","tls_key_file":"/private/key.pem"`, "", 1),
		strings.Replace(base, `"stun:stun.example.com:3478"`, `"turn:turn.example.com:3478"`, 1),
		strings.Replace(base, `"stun:stun.example.com:3478"`, `"stun:stun.example.com:3478?transport=sctp"`, 1),
		strings.Replace(base, `"stun:stun.example.com:3478"`, `"stun://user@stun.example.com:3478"`, 1),
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Fatalf("accepted invalid WebRTC config: %s", bad)
		}
	}
	relay := strings.Replace(base, `"stun:stun.example.com:3478"`, `"turn:turn.example.com:3478?transport=udp"`, 1)
	relay = strings.Replace(relay, `"webrtc":{"ice_servers":[{`, `"webrtc":{"ice_transport_policy":"relay","ice_servers":[{`, 1)
	relay = strings.Replace(relay, `"urls":["turn:turn.example.com:3478?transport=udp"]`, `"urls":["turn:turn.example.com:3478?transport=udp"],"username":"agent","credential_file":"/private/turn.secret"`, 1)
	if _, err := Parse([]byte(relay)); err != nil {
		t.Fatalf("valid relay WebRTC config=%v", err)
	}
}

func TestWebRTCCredentialsAreCheckedAsPrivateFiles(t *testing.T) {
	root := t.TempDir()
	credential := filepath.Join(root, "turn.secret")
	if err := os.WriteFile(credential, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	c := Defaults()
	c.Version, c.Realm = 1, "fixture"
	c.StateDir = filepath.Join(root, "state")
	c.Listeners = []Listener{
		{
			ID: "rtc", Protocol: "webrtc", Enabled: true, Address: "127.0.0.1:0",
			TLSCertFile: filepath.Join(root, "cert"), TLSKeyFile: filepath.Join(root, "key"),
			Operations: []core.Operation{core.Send},
			WebRTC:     &WebRTC{ICEServers: []WebRTCICE{{URLs: []string{"turn:turn.example.com:3478"}, Username: "agent", CredentialFile: credential}}},
		},
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := c.CheckFiles(); err == nil {
		t.Fatal("missing TLS files unexpectedly passed CheckFiles")
	}
	if err := os.WriteFile(filepath.Join(root, "cert"), []byte("cert"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "key"), []byte("key"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.CheckFiles(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(credential, 0644); err != nil {
		t.Fatal(err)
	}
	if err := c.CheckFiles(); err == nil {
		t.Fatal("group/world-readable TURN credential passed CheckFiles")
	}
}

func TestWebRTCStrictBoundsAndHostGrammar(t *testing.T) {
	for _, raw := range []string{
		"stun:-bad.example:3478",
		"stun:bad..example:3478",
		"stun:bad.example.:3478",
		"stun:bad.example:0",
		"stun:bad.example:65536",
	} {
		if _, err := validateWebRTCURL(raw); err == nil {
			t.Fatalf("accepted invalid ICE URL %q", raw)
		}
	}
	tooLong := strings.Repeat("a", 257)
	if err := validWebRTCUsername(tooLong); err == nil {
		t.Fatal("accepted oversized TURN username")
	}
	if err := validWebRTCUsername("user\nname"); err == nil {
		t.Fatal("accepted multiline TURN username")
	}
	if err := validateWebRTC(&WebRTC{ICEServers: []WebRTCICE{{URLs: []string{"turn:turn.example:3478"}, Username: "u"}}}); err == nil {
		t.Fatal("accepted TURN server without credential file")
	}
}
