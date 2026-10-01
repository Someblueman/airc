package protocol

import (
	"strings"
	"testing"
	"time"
)

func TestMessageMetadataRoundTripStaysWithinLineLimit(t *testing.T) {
	message := MessageMetadata{ID: "id", ReplyTo: strings.Repeat("a", 32), ThreadID: strings.Repeat("b", 32), From: "alice", Target: "#test", Message: strings.Repeat("\x01", 4096), Timestamp: time.Now().UTC()}
	encoded := EncodeMessageMetadata(message)
	if len(encoded) > MaxLineLength-128 {
		t.Fatalf("encoded metadata is unexpectedly large: %d bytes", len(encoded))
	}
	got, err := DecodeMessageMetadata(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != message.ID || got.ReplyTo != message.ReplyTo || got.ThreadID != message.ThreadID || got.From != message.From || got.Target != message.Target || got.Message != message.Message || !got.Timestamp.Equal(message.Timestamp) {
		t.Fatalf("decoded metadata = %#v, want %#v", got, message)
	}
}
