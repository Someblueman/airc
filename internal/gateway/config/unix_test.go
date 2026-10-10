package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestUnixListenerConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.sock")
	valid := strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"local","protocol":"unix","enabled":true,"address":"`+path+`","operations":["send"]}]`, 1)
	if !UnixAvailable() {
		if _, err := Parse([]byte(valid)); err == nil {
			t.Fatal("accepted enabled unix listener on unsupported platform")
		}
		return
	}
	if _, err := Parse([]byte(valid)); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		strings.Replace(valid, `"address":"`+path+`"`, `"address":"relative.sock"`, 1),
		strings.Replace(valid, `"address":"`+path+`"`, `"address":"`+filepath.Clean(path)+`/../gateway.sock"`, 1),
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Fatal("accepted invalid unix listener configuration")
		}
	}
}

func TestUnixListenerRejectsTLS(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.sock")
	input := strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"local","protocol":"unix","enabled":true,"address":"`+path+`","tls_cert_file":"/tmp/cert","tls_key_file":"/tmp/key","operations":["send"]}]`, 1)
	if _, err := Parse([]byte(input)); err == nil {
		t.Fatal("accepted TLS on unix listener")
	}
}
