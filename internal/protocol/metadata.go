package protocol

import (
	"encoding/base64"
	"encoding/json"
	"time"
)

type MessageMetadata struct {
	ID        string
	ReplyTo   string
	Reaction  string
	ThreadID  string
	Seq       uint64
	From      string
	Target    string
	Message   string
	Timestamp time.Time
}

type messageMetadataWire struct {
	ID        string    `json:"id"`
	ReplyTo   string    `json:"reply_to,omitempty"`
	Reaction  string    `json:"reaction,omitempty"`
	ThreadID  string    `json:"thread_id,omitempty"`
	Seq       uint64    `json:"seq,omitempty"`
	From      string    `json:"from"`
	Target    string    `json:"target"`
	Message   string    `json:"body_base64"`
	Timestamp time.Time `json:"timestamp"`
}

func EncodeMessageMetadata(message MessageMetadata) string {
	wire := messageMetadataWire{
		ID: message.ID, ReplyTo: message.ReplyTo, ThreadID: message.ThreadID, Reaction: message.Reaction, Seq: message.Seq, From: message.From, Target: message.Target,
		Message: base64.RawURLEncoding.EncodeToString([]byte(message.Message)), Timestamp: message.Timestamp,
	}
	encoded, _ := json.Marshal(wire)
	return string(encoded)
}

func DecodeMessageMetadata(encoded string) (MessageMetadata, error) {
	var wire messageMetadataWire
	if err := json.Unmarshal([]byte(encoded), &wire); err != nil {
		return MessageMetadata{}, err
	}
	body, err := base64.RawURLEncoding.DecodeString(wire.Message)
	if err != nil {
		return MessageMetadata{}, err
	}
	return MessageMetadata{ID: wire.ID, ReplyTo: wire.ReplyTo, ThreadID: wire.ThreadID, Reaction: wire.Reaction, Seq: wire.Seq, From: wire.From, Target: wire.Target, Message: string(body), Timestamp: wire.Timestamp}, nil
}
