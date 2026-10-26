package core

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestMessageIDPreservesHexAdmission(t *testing.T) {
	id := []byte(strings.Repeat("a", 32))
	for position := range id {
		for value := range 256 {
			id[position] = byte(value)
			_, err := hex.DecodeString(string(id))
			if got := messageID(string(id)); got != (err == nil) {
				t.Fatalf("position %d byte %02x: valid = %v, decoder error = %v", position, value, got, err)
			}
		}
		id[position] = 'a'
	}
	for _, id := range []string{"", "a", strings.Repeat("a", 31), strings.Repeat("a", 33), strings.Repeat("a", 64)} {
		if messageID(id) {
			t.Fatalf("accepted ID with %d bytes", len(id))
		}
	}
	if !messageID("0123456789abcdefABCDEF0123456789") {
		t.Fatal("rejected mixed hexadecimal case")
	}
	if !validPollAccountID("") || validPollAccountID("invalid") {
		t.Fatal("optional poll account ID contract changed")
	}
}
