package core

import (
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

func TestMessageMutationRequests(t *testing.T) {
	id := strings.Repeat("a", 32)
	for _, r := range []Request{
		{Version: 1, RequestID: "action", Operation: MessageAction, Target: &Target{Kind: "room", Name: "#room"}, Message: "hello"},
		{Version: 1, RequestID: "correct", Operation: MessageCorrect, Target: &Target{Kind: "room", Name: "#room"}, ParentID: id, Message: "fixed"},
		{Version: 1, RequestID: "retract", Operation: MessageRetract, Target: &Target{Kind: "room", Name: "#room"}, ParentID: id},
	} {
		if err := r.Normalize(); err != nil {
			t.Fatalf("%s: %v", r.Operation, err)
		}
	}
	for _, bad := range []Request{
		{Version: 1, RequestID: "bad-query", Operation: MessageCorrect, Target: &Target{Kind: "room", Name: "#room"}, ParentID: id, Message: "x", Query: "foreign"},
		{Version: 1, RequestID: "bad-reaction", Operation: MessageAction, Target: &Target{Kind: "room", Name: "#room"}, Message: "x", Reaction: "agree"},
	} {
		if err := bad.Normalize(); err == nil {
			t.Fatalf("accepted foreign argument for %s", bad.Operation)
		}
	}
}

func TestCorrectionReceiptValidation(t *testing.T) {
	id := strings.Repeat("a", 32)
	for _, tc := range []struct {
		op   Operation
		body string
	}{{MessageAction, "hello"}, {MessageCorrect, "fixed"}, {MessageRetract, ""}} {
		r := Request{Version: 1, RequestID: "request", Operation: tc.op, Target: &Target{Kind: "room", Name: "#room"}, Message: tc.body}
		if tc.op != MessageAction {
			r.ParentID = id
		}
		if err := r.Normalize(); err != nil {
			t.Fatal(err)
		}
		kind := "action"
		if tc.op == MessageCorrect {
			kind = "correct"
		} else if tc.op == MessageRetract {
			kind = "retract"
		}
		receipt := &irc.SendReceiptEvent{Type: "send_receipt", ChatMetadata: irc.ChatMetadata{AccountID: strings.Repeat("c", 32), Kind: kind, Supersedes: r.ParentID}, ID: strings.Repeat("b", 32), Seq: 1, From: "author", Target: "#room", Message: tc.body, Timestamp: time.Unix(1000, 0)}
		if tc.op == MessageRetract {
			receipt.Message = "Retracted"
		}
		if tc.op != MessageAction {
			receipt.ReplyTo = r.ParentID
			receipt.ThreadID = r.ParentID
		}
		receipt.Receipt = &irc.ReceiptInfo{Accepted: true}
		if _, err := ReceiptBytes(receipt, r, "ignored", "author"); err != nil {
			t.Fatalf("%s: %v", tc.op, err)
		}
	}
}

func TestMessageMutationReceiptRejectsForeignMetadata(t *testing.T) {
	id := strings.Repeat("a", 32)
	r := Request{Version: 1, RequestID: "action", Operation: MessageAction, Target: &Target{Kind: "room", Name: "#room"}, Message: "hello"}
	if err := r.Normalize(); err != nil {
		t.Fatal(err)
	}
	base := &irc.SendReceiptEvent{Type: "send_receipt", ChatMetadata: irc.ChatMetadata{AccountID: strings.Repeat("c", 32), Kind: "action"}, ID: strings.Repeat("b", 32), Seq: 1, From: "author", Target: "#room", Message: "hello", Timestamp: time.Unix(1000, 0), Receipt: &irc.ReceiptInfo{Accepted: true}}
	for name, mutate := range map[string]func(*irc.SendReceiptEvent){
		"missing account":   func(v *irc.SendReceiptEvent) { v.AccountID = "" },
		"uppercase account": func(v *irc.SendReceiptEvent) { v.AccountID = strings.Repeat("C", 32) },
		"zero sequence":     func(v *irc.SendReceiptEvent) { v.Seq = 0 },
		"superseded by":     func(v *irc.SendReceiptEvent) { v.SupersededBy = id },
		"retracted":         func(v *irc.SendReceiptEvent) { v.Retracted = true },
	} {
		v := *base
		mutate(&v)
		if _, err := ReceiptBytes(&v, r, "", "author"); err == nil {
			t.Fatalf("accepted %s", name)
		}
	}
}

func TestMessageMutationsRejectNoncanonicalNativeIDs(t *testing.T) {
	for _, op := range []Operation{MessageCorrect, MessageRetract} {
		r := Request{Version: 1, RequestID: "canonical-id", Operation: op, Target: &Target{Kind: "room", Name: "#room"}, ParentID: strings.Repeat("A", 32), Message: "text"}
		if err := r.Normalize(); err == nil {
			t.Fatal("uppercase native parent accepted")
		}
		r.ParentID = strings.Repeat("a", 32)
		if err := r.Normalize(); err != nil {
			t.Fatal(err)
		}
		kind := "correct"
		if op == MessageRetract {
			kind = "retract"
		}
		receipt := &irc.SendReceiptEvent{Type: "send_receipt", ChatMetadata: irc.ChatMetadata{AccountID: strings.Repeat("b", 32), Kind: kind, Supersedes: r.ParentID}, Receipt: &irc.ReceiptInfo{Accepted: true}, ID: strings.Repeat("c", 32), Seq: 1, From: "author", Target: "#room", Message: r.Message, ReplyTo: r.ParentID, ThreadID: r.ParentID, Timestamp: time.Now().UTC()}
		if _, err := ReceiptBytes(receipt, r, "", "author"); err != nil {
			t.Fatal(err)
		}
		receipt.ID = strings.ToUpper(receipt.ID)
		if _, err := ReceiptBytes(receipt, r, "", "author"); err == nil {
			t.Fatal("uppercase receipt ID accepted")
		}
		receipt.ID = strings.ToLower(receipt.ID)
		receipt.ThreadID = strings.ToUpper(receipt.ThreadID)
		if _, err := ReceiptBytes(receipt, r, "", "author"); err == nil {
			t.Fatal("uppercase receipt root accepted")
		}
	}
}
