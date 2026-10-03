package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestMessageMetadataRoundTripStaysWithinLineLimit(t *testing.T) {
	message := MessageMetadata{ID: "id", ReplyTo: strings.Repeat("a", 32), ThreadID: strings.Repeat("b", 32), Reaction: "checking", From: "alice", Target: "#test", Message: strings.Repeat("\x01", 4096), Timestamp: time.Now().UTC()}
	encoded := EncodeMessageMetadata(message)
	if len(encoded) > MaxLineLength-128 {
		t.Fatalf("encoded metadata is unexpectedly large: %d bytes", len(encoded))
	}
	got, err := DecodeMessageMetadata(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != message.ID || got.ReplyTo != message.ReplyTo || got.ThreadID != message.ThreadID || got.Reaction != message.Reaction || got.From != message.From || got.Target != message.Target || got.Message != message.Message || !got.Timestamp.Equal(message.Timestamp) {
		t.Fatalf("decoded metadata = %#v, want %#v", got, message)
	}
}

func TestMetadataNestedJSONPreservesWireAndRejectsInvalidBody(t *testing.T) {
	m := MessageMetadata{ID: "id", From: "alice", Target: "#room", Message: "hello\n🙂", Timestamp: time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)}
	const wire = `{"id":"id","from":"alice","target":"#room","body_base64":"aGVsbG8K8J-Zgg","timestamp":"2026-10-03T00:00:00Z"}`
	if got := EncodeMessageMetadata(m); got != wire {
		t.Fatalf("wire changed: %s", got)
	}
	data, err := json.Marshal(struct {
		Message MessageMetadata `json:"message"`
	}{m})
	if err != nil || string(data) != `{"message":`+wire+`}` {
		t.Fatalf("nested wire changed: %s %v", data, err)
	}
	var decoded struct {
		Message MessageMetadata `json:"message"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil || !reflect.DeepEqual(decoded.Message, m) {
		t.Fatalf("nested round trip: %+v %v", decoded, err)
	}
	for _, invalid := range []string{`{"body_base64":"%%%"}`, `{"timestamp":"invalid"}`, `{`} {
		before := decoded.Message
		if err := json.Unmarshal([]byte(invalid), &decoded.Message); err == nil || !reflect.DeepEqual(decoded.Message, before) {
			t.Fatalf("invalid metadata changed destination: %s, %v", invalid, err)
		}
	}
}
