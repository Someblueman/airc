package core

import "time"

const MaxTopicBytes = 4 << 10

type TopicResult struct {
	Target Target    `json:"target"`
	Topic  string    `json:"topic"`
	SetBy  string    `json:"set_by,omitempty"`
	SetAt  time.Time `json:"set_at,omitzero"`
}
