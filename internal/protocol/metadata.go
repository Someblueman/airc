package protocol

import (
	"encoding/base64"
	"encoding/json"
	"time"
)

type MessageMetadata struct {
	ID        string
	From      string
	Target    string
	Message   string
	Timestamp time.Time
}

type messageMetadataWire struct {
	ID        string    `json:"id"`
	From      string    `json:"from"`
	Target    string    `json:"target"`
	Message   string    `json:"body_base64"`
	Timestamp time.Time `json:"timestamp"`
}

func EncodeMessageMetadata(message MessageMetadata) string {
	wire := messageMetadataWire{
		ID: message.ID, From: message.From, Target: message.Target,
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
	return MessageMetadata{ID: wire.ID, From: wire.From, Target: wire.Target, Message: string(body), Timestamp: wire.Timestamp}, nil
}
