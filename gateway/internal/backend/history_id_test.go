package backend

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestValidHistoryIDPreservesHexAdmission(t *testing.T) {
	// History accepts both hexadecimal cases; canonical command IDs are a
	// different boundary. Compare every possible byte in every ID position
	// against the original decoder, including malformed UTF-8 and controls.
	id := []byte(strings.Repeat("a", 32))
	for position := range id {
		for value := range 256 {
			id[position] = byte(value)
			_, err := hex.DecodeString(string(id))
			if got := validHistoryID(string(id)); got != (err == nil) {
				t.Fatalf("position %d byte %02x: valid = %v, decoder error = %v", position, value, got, err)
			}
		}
		id[position] = 'a'
	}
	for _, id := range []string{"", "a", strings.Repeat("a", 31), strings.Repeat("a", 33), strings.Repeat("a", 64)} {
		if validHistoryID(id) {
			t.Fatalf("accepted ID with %d bytes", len(id))
		}
	}
	if !validHistoryID("0123456789abcdefABCDEF0123456789") {
		t.Fatal("rejected mixed hexadecimal case")
	}
}
