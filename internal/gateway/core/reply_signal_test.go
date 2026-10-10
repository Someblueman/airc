package core

import (
	"strings"
	"testing"
	"time"
)

func replySignalRequest(op Operation, target Target, input *ReplySignalInput) Request {
	return Request{Version: 1, RequestID: "reply-signal", Operation: op, Target: &target, ParentID: strings.Repeat("a", 32), ReplySignal: input}
}

func TestReplySignalNormalizeAndAck(t *testing.T) {
	input := &ReplySignalInput{Note: "working", TTLSeconds: 30}
	r := replySignalRequest(ReplySignalSet, Target{Kind: "direct", Name: "Peer"}, input)
	if err := r.Normalize(); err != nil {
		t.Fatal(err)
	}
	expiry := time.Unix(1700000000, 0).UTC()
	ack := &ReplySignalAcknowledgement{Version: 1, Operation: ReplySignalSet, Target: *r.Target, ParentID: r.ParentID, NativeTarget: "Peer", Action: "prepare", From: "Author", AccountID: strings.Repeat("a", 32), Note: "prior note", ExpiresAt: expiry}
	if _, err := ReplySignalAckBytes(ack, r, "Author"); err != nil {
		t.Fatal(err)
	}
}

func TestReplySignalRejectsForeignAndInvalidFields(t *testing.T) {
	cases := []Request{
		replySignalRequest(ReplySignalSet, Target{Kind: "room", Name: "#Allowed"}, &ReplySignalInput{TTLSeconds: 0}),
		func() Request {
			r := replySignalRequest(ReplySignalCancel, Target{Kind: "room", Name: "#Allowed"}, nil)
			r.Message = "x"
			return r
		}(),
		func() Request {
			r := replySignalRequest(ReplySignalSet, Target{Kind: "room", Name: "#Allowed"}, &ReplySignalInput{TTLSeconds: 1})
			r.Query = "x"
			return r
		}(),
		func() Request {
			r := replySignalRequest(ReplySignalSet, Target{Kind: "room", Name: "#Allowed"}, &ReplySignalInput{TTLSeconds: 1})
			r.ParentID = "bad"
			return r
		}(),
	}
	for i, r := range cases {
		if err := r.Normalize(); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
}

func TestReplySignalAckRejectsIdentityAndNativeTarget(t *testing.T) {
	r := replySignalRequest(ReplySignalCancel, Target{Kind: "room", Name: "#Allowed"}, nil)
	if err := r.Normalize(); err != nil {
		t.Fatal(err)
	}
	ack := &ReplySignalAcknowledgement{Version: 1, Operation: ReplySignalCancel, Target: *r.Target, ParentID: r.ParentID, NativeTarget: "#Other", Action: "cancel", From: "Author", ExpiresAt: time.Unix(1700000000, 0).UTC()}
	if _, err := ReplySignalAckBytes(ack, r, "Author"); err == nil {
		t.Fatal("accepted foreign native target")
	}
	ack.NativeTarget = "#Allowed"
	ack.From = "Other"
	if _, err := ReplySignalAckBytes(ack, r, "Author"); err == nil {
		t.Fatal("accepted foreign author")
	}
	ack.From = "Author"
	ack.NativeTarget = " #Allowed"
	if _, err := ReplySignalAckBytes(ack, r, "Author"); err == nil {
		t.Fatal("accepted whitespace native target")
	}
	ack.NativeTarget = "#Allowed"
	if _, err := ReplySignalAckBytes(ack, r, "Author"); err != nil {
		t.Fatal("rejected native cancellation timestamp", err)
	}
}
