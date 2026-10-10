package protocol

import (
	"strings"
	"testing"
)

func TestValidSource(t *testing.T) {
	for _, source := range []string{"203.0.113.9", "2001:db8::1", "::1", "listener:mqtt", "route:redis-requests", "a", strings.Repeat("a", MaxSourceLength)} {
		if !ValidSource(source) {
			t.Errorf("%q refused", source)
		}
	}
	for _, source := range []string{"", "203.0.113.9:1234", "[::1]:80", "fe80::1%eth0", ":x", "-x", "1abc", "a b", "a\x00", "listener:mqtt\r\n", strings.Repeat("a", MaxSourceLength+1)} {
		if ValidSource(source) {
			t.Errorf("%q accepted", source)
		}
	}
}
