package protocol

import (
	"strings"
	"testing"
)

func TestValidIDArgumentAcceptsFullIDsAndPrefixesOfEightOrMore(t *testing.T) {
	full := strings.Repeat("3a13b80f", 4)
	for _, arg := range []string{full, full[:8], full[:9], full[:31], "00000000", "ffffffff"} {
		if !ValidIDArgument(arg) {
			t.Errorf("%q should be a valid ID argument", arg)
		}
	}
	for _, arg := range []string{"", full[:7], "3a13b80", full + "0", strings.ToUpper(full[:8]), "3a13b80g", "3a13b80f ", "last"} {
		if ValidIDArgument(arg) {
			t.Errorf("%q should not be a valid ID argument", arg)
		}
	}
	// ValidMessageID stays the test for an emitted ID.
	if !ValidMessageID(full) || ValidMessageID(full[:8]) || ValidMessageID(full[:31]) || ValidMessageID(strings.ToUpper(full)) {
		t.Error("ValidMessageID accepts exactly 32 lowercase hex characters")
	}
}

func TestConversationTargetAcceptsAbbreviatedIDs(t *testing.T) {
	full := strings.Repeat("ab", 16)
	for _, tc := range []struct {
		target      string
		id          string
		replies, ok bool
	}{
		{"thread:" + full, full, false, true},
		{"replies:" + full, full, true, true},
		{"thread:" + full[:8], full[:8], false, true},
		{"replies:" + full[:12], full[:12], true, true},
		{"thread:" + full[:7], full[:7], false, false},
		{"thread:", "", false, false},
		{"#room", "#room", true, false},
		{full, full, true, false},
	} {
		id, replies, ok := ConversationTarget(tc.target)
		if id != tc.id || replies != tc.replies || ok != tc.ok {
			t.Errorf("ConversationTarget(%q) = %q %v %v, want %q %v %v", tc.target, id, replies, ok, tc.id, tc.replies, tc.ok)
		}
	}
}
