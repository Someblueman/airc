//go:build darwin || linux

package store

import (
	"github.com/Someblueman/airc/pkg/irc"
	"strings"
	"time"
)

func receiptFor(a Attempt) *irc.SendReceiptEvent {
	connected := false
	return &irc.SendReceiptEvent{Type: "send_receipt", ChatMetadata: irc.ChatMetadata{RequestID: a.Job.UpstreamID, AccountID: "fixture-account"}, ID: strings.Repeat("a", 32), From: "author", Target: a.Job.Request.Target.Name, Message: a.Job.Request.Message, Timestamp: time.Unix(1000, 123), Receipt: &irc.ReceiptInfo{Accepted: true, Persisted: false, RecipientConnected: &connected}}
}
