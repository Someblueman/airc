package matrixwire

import (
	"strings"
	"testing"
)

func TestMatrixUserIdentifiers(t *testing.T) {
	for _, s := range []string{"@alice:example.org", "@a._=-/+:EXAMPLE.org:8448", "@alice:127.0.0.1", "@alice:[::1]", "@alice:[2001:db8::1]:8448", "@" + strings.Repeat("a", 242) + ":example.org"} {
		if !UserID(s) {
			t.Fatal("valid identifier refused", s)
		}
	}
	for _, s := range []string{"", "@a", "@Alice:example.org", "@:example.org", "@a:", "@a:example.org/path", "@a:example.org:0", "@a:example.org:65536", "@a:example.org:+443", "@a:example.org:00000443", "@a:[::1]:00000443", "@a:::1", "@a:[::1%lo0]", "@a:[127.0.0.1]", "@a:bad..domain", "@a:-bad.org", "@a:bad-.org", "@a:é.org", "@a:bad\n.org", "@" + strings.Repeat("a", 243) + ":example.org"} {
		if UserID(s) {
			t.Fatal("invalid identifier accepted", s)
		}
	}
}

func TestMatrixOpaqueRoomAndEventIdentifiers(t *testing.T) {
	for _, sigil := range []byte{'!', '$'} {
		validate := RoomID
		if sigil == '$' {
			validate = EventID
		}
		for _, tail := range []string{"AbC123", "legacy:example.org", "Hash_-abc", "世界", "a/b+c", strings.Repeat("x", 254)} {
			s := string(sigil) + tail
			if !validate(s) {
				t.Fatal("opaque identifier refused", s)
			}
		}
		for _, tail := range []string{"", "a b", "a\n", "a\x00", "a\u00a0", "\xff", strings.Repeat("x", 255)} {
			if validate(string(sigil) + tail) {
				t.Fatal("invalid opaque identifier accepted", tail)
			}
		}
		if validate("#room:example.org") || validate("@alice:example.org") {
			t.Fatal("alias/user treated as room/event")
		}
	}
}
