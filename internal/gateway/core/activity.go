package core

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
)

type ActivityInput struct {
	Kind       string `json:"kind"`
	Note       string `json:"note,omitempty"`
	TTLSeconds int64  `json:"ttl_seconds"`
}

type ActivityAcknowledgement struct {
	Version   int       `json:"version"`
	Operation Operation `json:"operation"`
	Target    Target    `json:"target"`
	Kind      string    `json:"kind"`
	From      string    `json:"from"`
	AccountID string    `json:"account_id"`
	Note      string    `json:"note,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
}

const MaxActivityAckBytes = 4096

func (a *ActivityInput) valid() bool {
	return a != nil && (a.Kind == "typing" || a.Kind == "thinking") && a.TTLSeconds >= 1 && a.TTLSeconds <= 15 && protocol.BriefText(a.Note, 240)
}

func validActivityAccountID(s string) bool {
	if len(s) != 32 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func ActivityAckBytes(ack *ActivityAcknowledgement, request Request, author string) ([]byte, error) {
	if ack == nil || ack.Version != 1 || ack.Operation != ActivitySet || request.Operation != ActivitySet || request.Target == nil || (request.Target.Kind != "room" && request.Target.Kind != "direct") || request.Activity == nil || author == "" {
		return nil, errors.New("invalid activity acknowledgement")
	}
	originalTarget := *request.Target
	target := originalTarget
	activity := *request.Activity
	normalized := request
	normalized.Target = &target
	normalized.Activity = &activity
	authorTarget := Target{Kind: "direct", Name: author}
	if err := authorTarget.Normalize(); err != nil || strings.TrimSpace(author) != author {
		return nil, errors.New("invalid activity author")
	}
	if err := normalized.Normalize(); err != nil || originalTarget != *normalized.Target || ack.Target != *normalized.Target || ack.Operation != normalized.Operation || ack.From != author || !validActivityAccountID(ack.AccountID) || (ack.Kind != "typing" && ack.Kind != "thinking") || !protocol.BriefText(ack.Note, 240) || ack.ExpiresAt.IsZero() {
		return nil, errors.New("invalid activity acknowledgement")
	}
	if _, err := ack.ExpiresAt.MarshalJSON(); err != nil {
		return nil, errors.New("invalid activity timestamp")
	}
	data, err := json.Marshal(ack)
	if err != nil || len(data) > MaxActivityAckBytes {
		return nil, errors.New("invalid activity acknowledgement")
	}
	return data, nil
}
