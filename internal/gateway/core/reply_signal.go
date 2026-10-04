package core

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
)

const (
	ReplySignalSet    Operation = "reply_signal.set"
	ReplySignalCancel Operation = "reply_signal.cancel"
)

type ReplySignalInput struct {
	Note       string `json:"note,omitempty"`
	TTLSeconds int64  `json:"ttl_seconds"`
}

type ReplySignalAcknowledgement struct {
	Version      int       `json:"version"`
	Operation    Operation `json:"operation"`
	Target       Target    `json:"target"`
	ParentID     string    `json:"parent_id"`
	NativeTarget string    `json:"native_target"`
	Action       string    `json:"action"`
	From         string    `json:"from"`
	AccountID    string    `json:"account_id,omitempty"`
	Note         string    `json:"note,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
}

const MaxReplySignalAckBytes = 4096

func (i *ReplySignalInput) valid() bool {
	return i != nil && i.TTLSeconds >= 1 && i.TTLSeconds <= 900 && protocol.BriefText(i.Note, 240)
}

func validReplySignalAccountID(s string) bool {
	if len(s) != 32 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func validAuthor(author string) bool {
	if author == "" || strings.TrimSpace(author) != author {
		return false
	}
	t := Target{Kind: "direct", Name: author}
	return t.Normalize() == nil
}

func ReplySignalAckBytes(ack *ReplySignalAcknowledgement, request Request, author string) ([]byte, error) {
	if ack == nil || ack.Version != 1 || (ack.Operation != ReplySignalSet && ack.Operation != ReplySignalCancel) || request.Operation != ack.Operation || request.Target == nil || request.Target.Kind != "room" && request.Target.Kind != "direct" || !messageID(request.ParentID) || !validAuthor(author) {
		return nil, errors.New("invalid reply signal acknowledgement")
	}
	originalTarget := *request.Target
	originalParent := request.ParentID
	normalized := request
	target := originalTarget
	normalized.Target = &target
	var input ReplySignalInput
	if request.ReplySignal != nil {
		input = *request.ReplySignal
		normalized.ReplySignal = &input
	}
	if err := normalized.Normalize(); err != nil || originalTarget != *normalized.Target || originalParent != normalized.ParentID || ack.Target != *normalized.Target || ack.ParentID != originalParent || ack.Operation != normalized.Operation || ack.From != author {
		return nil, errors.New("invalid reply signal acknowledgement")
	}
	if ack.Target.Kind == "room" {
		if ack.NativeTarget != ack.Target.Name {
			return nil, errors.New("invalid native reply target")
		}
	} else {
		native := Target{Kind: "direct", Name: ack.NativeTarget}
		if strings.TrimSpace(ack.NativeTarget) != ack.NativeTarget || native.Normalize() != nil || (native.Name != ack.Target.Name && !strings.EqualFold(native.Name, author)) {
			return nil, errors.New("invalid native reply target")
		}
	}
	switch ack.Operation {
	case ReplySignalSet:
		if !request.ReplySignal.valid() || ack.Action != "prepare" || !validReplySignalAccountID(ack.AccountID) || !protocol.BriefText(ack.Note, 240) || ack.ExpiresAt.IsZero() {
			return nil, errors.New("invalid reply signal set acknowledgement")
		}
		if _, err := ack.ExpiresAt.MarshalJSON(); err != nil {
			return nil, errors.New("invalid reply signal timestamp")
		}
	case ReplySignalCancel:
		if request.ReplySignal != nil || ack.Action != "cancel" || ack.AccountID != "" || ack.Note != "" || ack.ExpiresAt.IsZero() {
			return nil, errors.New("invalid reply signal cancel acknowledgement")
		}
	}
	data, err := json.Marshal(ack)
	if err != nil || len(data) > MaxReplySignalAckBytes {
		return nil, errors.New("invalid reply signal acknowledgement")
	}
	return data, nil
}
