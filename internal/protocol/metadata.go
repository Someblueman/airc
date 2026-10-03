package protocol

import (
	"encoding/base64"
	"encoding/json"
	"time"
)

type ReceiptInfo struct {
	Accepted           bool  `json:"accepted"`
	Persisted          bool  `json:"persisted"`
	RecipientConnected *bool `json:"recipient_connected,omitempty"`
}

type MessageMetadata struct {
	Receipt *ReceiptInfo
	ChatMetadata
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
	Receipt *ReceiptInfo `json:"receipt,omitempty"`
	ChatMetadata
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

// Nested JSON uses bytes directly; routing through the public string helpers
// needlessly copied every large metadata object twice in context/CHECK streams.
func (message MessageMetadata) MarshalJSON() ([]byte, error) {
	wire := messageMetadataWire{
		Receipt: message.Receipt, ChatMetadata: message.ChatMetadata,
		ID: message.ID, ReplyTo: message.ReplyTo, ThreadID: message.ThreadID, Reaction: message.Reaction, Seq: message.Seq, From: message.From, Target: message.Target,
		Message: base64.RawURLEncoding.EncodeToString([]byte(message.Message)), Timestamp: message.Timestamp,
	}
	return json.Marshal(wire)
}

func (m *MessageMetadata) UnmarshalJSON(data []byte) error {
	var wire messageMetadataWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	body, err := base64.RawURLEncoding.DecodeString(wire.Message)
	if err != nil {
		return err
	}
	*m = MessageMetadata{Receipt: wire.Receipt, ChatMetadata: wire.ChatMetadata, ID: wire.ID, ReplyTo: wire.ReplyTo, ThreadID: wire.ThreadID, Reaction: wire.Reaction, Seq: wire.Seq, From: wire.From, Target: wire.Target, Message: string(body), Timestamp: wire.Timestamp}
	return nil
}

func EncodeMessageMetadata(message MessageMetadata) string {
	encoded, _ := message.MarshalJSON()
	return string(encoded)
}

func DecodeMessageMetadata(encoded string) (MessageMetadata, error) {
	var message MessageMetadata
	err := message.UnmarshalJSON([]byte(encoded))
	return message, err
}
