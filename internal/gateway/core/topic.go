package core

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
)

const MaxTopicBytes = 4 << 10

type TopicResult struct {
	Target Target    `json:"target"`
	Topic  string    `json:"topic"`
	SetBy  string    `json:"set_by,omitempty"`
	SetAt  time.Time `json:"set_at,omitzero"`
}

type TopicAcknowledgement struct {
	Version   int         `json:"version"`
	Operation Operation   `json:"operation"`
	Topic     TopicResult `json:"topic"`
}

const MaxTopicAcknowledgementBytes = 4 << 10

func TopicAckBytes(ack *TopicAcknowledgement, request Request, author string) ([]byte, error) {
	if ack == nil || ack.Version != 1 || ack.Operation != TopicSet || request.Operation != TopicSet || request.Target == nil || request.Target.Kind != "room" || request.Topic == nil || author == "" {
		return nil, errors.New("invalid topic acknowledgement")
	}
	normalized := request
	target := *request.Target
	topic := *request.Topic
	normalized.Target = &target
	normalized.Topic = &topic
	if err := normalized.Normalize(); err != nil || normalized.Topic == nil || topic != *normalized.Topic || target != *normalized.Target || strings.TrimSpace(*normalized.Topic) != *normalized.Topic || !protocol.BriefText(*normalized.Topic, 400) || ack.Topic.Target != *normalized.Target || ack.Topic.Topic != *normalized.Topic {
		return nil, errors.New("invalid topic acknowledgement")
	}
	if err := (&Target{Kind: "direct", Name: author}).Normalize(); err != nil {
		return nil, errors.New("invalid topic author")
	}
	if *request.Topic == "" {
		if ack.Topic.SetBy != "" || !ack.Topic.SetAt.IsZero() {
			return nil, errors.New("invalid cleared topic acknowledgement")
		}
	} else {
		if ack.Topic.SetBy != author || ack.Topic.SetAt.IsZero() || !protocol.BriefText(ack.Topic.Topic, 400) {
			return nil, errors.New("invalid topic acknowledgement metadata")
		}
		if _, err := ack.Topic.SetAt.MarshalJSON(); err != nil {
			return nil, errors.New("invalid topic timestamp")
		}
	}
	data, err := json.Marshal(ack)
	if err != nil || len(data) > MaxTopicAcknowledgementBytes {
		return nil, errors.New("invalid topic acknowledgement")
	}
	return data, nil
}
