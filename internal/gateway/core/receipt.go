package core

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

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

// ReceiptBytes validates SDK evidence against the immutable stored intent.
func ReceiptBytes(r *irc.SendReceiptEvent, request Request, upstreamID, author string) ([]byte, error) {
	if request.Target == nil || request.Operation != Send && request.Operation != Reply || r == nil || r.Type != "send_receipt" || r.Receipt == nil || !r.Receipt.Accepted || !validMessageID(r.ID) || r.RequestID != upstreamID || r.Message != request.Message || r.Reaction != "" || r.Timestamp.IsZero() {
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
	if request.Operation == Reply {
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
