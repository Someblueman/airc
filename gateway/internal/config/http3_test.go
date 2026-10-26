package config

import (
	"strings"
	"testing"
)

func TestHTTP3RequiresTLSAndExistingReadPermissions(t *testing.T) {
	listener := `"listeners":[{"id":"http3","protocol":"http3","enabled":true,"tls_cert_file":"/private/gateway/cert.pem","tls_key_file":"/private/gateway/key.pem","operations":["send","receipt.get"]}]`
	input := strings.Replace(emptyConfig, `"listeners":[]`, listener, 1)
	c, err := Parse([]byte(input))
	if err != nil || c.Listeners[0].Address != "127.0.0.1:0" {
		t.Fatal(c, err)
	}
	for _, bad := range []string{
		strings.Replace(input, `,"tls_cert_file":"/private/gateway/cert.pem","tls_key_file":"/private/gateway/key.pem"`, "", 1),
		strings.Replace(input, `"receipt.get"`, `"watch"`, 1),
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Fatal("http3 bypassed TLS, cursor or origin boundary")
		}
	}
	if _, err := Parse([]byte(strings.Replace(input, `"enabled":true`, `"enabled":true,"allowed_origins":["https://example.com"]`, 1))); err != nil {
		t.Fatal(err)
	}
	watch := strings.Replace(input, `"receipt.get"`, `"events.watch"`, 1)
	watch = strings.Replace(watch, `"version":1`, `"version":1,"cursor_key_file":"/private/gateway/cursor.key"`, 1)
	if _, err := Parse([]byte(watch)); err != nil {
		t.Fatal(err)
	}
}
