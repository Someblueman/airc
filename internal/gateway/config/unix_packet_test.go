package config

import (
	"strings"
	"testing"
)

func TestUnixPacketConfiguration(t *testing.T) {
	input := strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"local","protocol":"unixpacket","enabled":false,"address":"/tmp/gateway.sock","operations":["send"]}]`, 1)
	if _, err := Parse([]byte(input)); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse([]byte(strings.Replace(input, "/tmp/gateway.sock", "/"+strings.Repeat("x", 106), 1))); err != nil {
		t.Fatal("disabled Linux inventory rejected its exact 107-byte bound", err)
	}
	enabled := strings.Replace(input, `"enabled":false`, `"enabled":true`, 1)
	_, err := Parse([]byte(enabled))
	if UnixPacketAvailable() && err != nil || !UnixPacketAvailable() && err == nil {
		t.Fatalf("enabled platform gate: %v", err)
	}
	for _, path := range []string{"", "relative.sock", "/tmp/../gateway.sock", "/" + strings.Repeat("x", 107)} {
		bad := strings.Replace(input, "/tmp/gateway.sock", path, 1)
		if _, err := Parse([]byte(bad)); err == nil {
			t.Fatalf("accepted path %q", path)
		}
	}
	for _, extra := range []string{`"tls_cert_file":"/tmp/cert","tls_key_file":"/tmp/key",`, `"allowed_origins":["https://example.com"],`} {
		bad := strings.Replace(enabled, `"operations":`, extra+`"operations":`, 1)
		if _, err := Parse([]byte(bad)); err == nil {
			t.Fatal("accepted TLS or origins")
		}
	}
}
