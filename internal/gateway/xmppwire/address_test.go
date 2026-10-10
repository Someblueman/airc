package xmppwire

import (
	"strings"
	"testing"
)

func TestCanonicalProvisionedJIDs(t *testing.T) {
	for _, s := range []string{"alice@example.com", "room-1@muc.example.com", strings.Repeat("a", 64) + "@" + strings.Repeat("b", 63) + ".example"} {
		if !BareJID(s) {
			t.Fatal("valid address refused:", s)
		}
	}
	for _, s := range []string{"", "alice", "@example.com", "alice@", "Alice@example.com", "alice@Example.com", "alice@example.com.", "alice@-example.com", "alice@example..com", "alice@a_b", "alice@世界", "世界@example.com", "alice@example.com/resource", "a@b@c", strings.Repeat("a", 65) + "@example.com", "a@" + strings.Repeat("b", 64)} {
		if BareJID(s) {
			t.Fatal("unsafe/alias address accepted:", s)
		}
	}
	bare, resource, ok := FullJID("alice@example.com/CaseSensitive-1")
	if !ok || bare != "alice@example.com" || resource != "CaseSensitive-1" {
		t.Fatal(bare, resource, ok)
	}
	for _, s := range []string{"alice@example.com", "alice@example.com/", "Alice@example.com/r", "alice@example.com/a/b", "alice@example.com/世界", "alice@example.com/a b", "alice@example.com/" + strings.Repeat("a", 65)} {
		if _, _, ok := FullJID(s); ok {
			t.Fatal("invalid full address accepted:", s)
		}
	}
}
