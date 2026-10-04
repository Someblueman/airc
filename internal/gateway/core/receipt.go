package core

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

const MaxReceiptBytes = 64 << 10

var ErrInvalidReceipt = errors.New("invalid_acceptance_receipt")

func validMessageID(s string) bool {
	if len(s) != 32 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func validMessageAccountID(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// ReceiptBytes validates SDK evidence against the immutable stored intent.
func ReceiptBytes(r *irc.SendReceiptEvent, request Request, upstreamID, author string) ([]byte, error) {
	if request.Target == nil || request.Operation != Send && request.Operation != Reply && request.Operation != ReactionAdd && request.Operation != MessageAction && request.Operation != MessageCorrect && request.Operation != MessageRetract || r == nil || r.Type != "send_receipt" || r.Receipt == nil || !r.Receipt.Accepted || !validMessageID(r.ID) || r.Timestamp.IsZero() {
		return nil, ErrInvalidReceipt
	}
	if request.Operation == ReactionAdd {
		if r.RequestID != "" || r.Kind != "" || r.Message != request.Reaction || r.Reaction != request.Reaction || request.Target.Kind != "room" {
			return nil, ErrInvalidReceipt
		}
	} else if request.Operation == MessageAction || request.Operation == MessageCorrect || request.Operation == MessageRetract {
		expected := request.Message
		if request.Operation == MessageRetract && expected == "" {
			expected = "Retracted"
		}
		expectedKind := "action"
		if request.Operation == MessageCorrect {
			expectedKind = "correct"
		} else if request.Operation == MessageRetract {
			expectedKind = "retract"
		}
		if r.RequestID != "" || r.Message != expected || r.Reaction != "" || r.Kind != expectedKind || r.Supersedes != request.ParentID || r.SupersededBy != "" || r.Retracted || !protocol.ValidMessageID(r.ID) || r.AccountID == "" || !validMessageAccountID(r.AccountID) || r.Seq == 0 {
			return nil, ErrInvalidReceipt
		}
		if request.Target.Kind == "room" {
			if r.Queued || r.Receipt.RecipientConnected != nil {
				return nil, ErrInvalidReceipt
			}
		} else if r.Receipt.RecipientConnected == nil || *r.Receipt.RecipientConnected == r.Queued {
			return nil, ErrInvalidReceipt
		}
	} else if r.RequestID != upstreamID || r.Message != request.Message || r.Reaction != "" {
		return nil, ErrInvalidReceipt
	}
	if len(r.AccountID) > 128 || len(r.Kind) > 64 || len(r.Supersedes) > 64 || len(r.SupersededBy) > 64 || len(r.PollOptions) != 0 || !r.PollClosesAt.IsZero() {
		return nil, ErrInvalidReceipt
	}
	from := Target{Kind: "direct", Name: r.From}
	if err := from.Normalize(); err != nil || author == "" || from.Name != strings.ToLower(author) {
		return nil, ErrInvalidReceipt
	}
	target := Target{Kind: request.Target.Kind, Name: r.Target}
	if err := target.Normalize(); err != nil || target != *request.Target {
		return nil, ErrInvalidReceipt
	}
	if request.Operation == MessageCorrect || request.Operation == MessageRetract {
		if r.ReplyTo != request.ParentID || !protocol.ValidMessageID(r.ThreadID) {
			return nil, ErrInvalidReceipt
		}
	} else if request.Operation == Reply || request.Operation == ReactionAdd {
		if r.ReplyTo != request.ParentID || !validMessageID(r.ThreadID) {
			return nil, ErrInvalidReceipt
		}
	} else if r.ReplyTo != "" || r.ThreadID != "" && r.ThreadID != r.ID {
		return nil, ErrInvalidReceipt
	}
	data, err := json.Marshal(r)
	if err != nil || len(data) > MaxReceiptBytes {
		return nil, ErrInvalidReceipt
	}
	return data, nil
}
