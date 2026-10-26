package core

import (
	"strings"
	"testing"
	"time"
)

func TestActivityNormalizeAndAcknowledgement(t *testing.T) {
	r := Request{Version: 1, RequestID: "activity", Operation: ActivitySet, Target: &Target{Kind: "direct", Name: "Alice"}, Activity: &ActivityInput{Kind: "typing", Note: "working", TTLSeconds: 5}}
	if err := r.Normalize(); err != nil || r.Target.Name != "alice" {
		t.Fatal(err)
	}
	ack := &ActivityAcknowledgement{Version: 1, Operation: ActivitySet, Target: *r.Target, Kind: "thinking", From: "agent", AccountID: strings.Repeat("a", 32), Note: "other", ExpiresAt: time.Unix(100, 0)}
	if _, err := ActivityAckBytes(ack, r, "agent"); err != nil {
		t.Fatal(err)
	}
	ack.From = "other"
	if _, err := ActivityAckBytes(ack, r, "agent"); err == nil {
		t.Fatal("accepted activity identity mismatch")
	}
	ack.From = "agent"
	badTarget := *r.Target
	badTarget.Name += " "
	badRequest := r
	badRequest.Target = &badTarget
	if _, err := ActivityAckBytes(ack, badRequest, "agent"); err == nil {
		t.Fatal("accepted unnormalized activity target")
	}
	if _, err := ActivityAckBytes(ack, r, "bad nick"); err == nil {
		t.Fatal("accepted invalid activity author")
	}
	ack.From = "Author"
	if _, err := ActivityAckBytes(ack, r, "Author"); err != nil {
		t.Fatalf("rejected valid mixed-case author: %v", err)
	}
}

func TestActivityBoundsAndForeignFields(t *testing.T) {
	for _, input := range []*ActivityInput{nil, {Kind: "typing", TTLSeconds: 0}, {Kind: "typing", TTLSeconds: 16}, {Kind: "away", TTLSeconds: 1}, {Kind: "typing", TTLSeconds: 1, Note: strings.Repeat("x", 241)}} {
		r := Request{Version: 1, RequestID: "activity", Operation: ActivitySet, Target: &Target{Kind: "room", Name: "#room"}, Activity: input}
		if err := r.Normalize(); err == nil {
			t.Fatal("accepted invalid activity")
		}
	}
	for _, mutate := range []func(*Request){
		func(r *Request) { r.Message = "message" },
		func(r *Request) { r.Reaction = "agree" },
		func(r *Request) { r.ParentID = strings.Repeat("a", 32) },
		func(r *Request) { r.Activity = nil },
	} {
		activity := &ActivityInput{Kind: "typing", TTLSeconds: 1}
		r := Request{Version: 1, RequestID: "activity", Operation: ActivitySet, Target: &Target{Kind: "room", Name: "#room"}, Activity: activity}
		mutate(&r)
		if err := r.Normalize(); err == nil {
			t.Fatal("accepted foreign activity field")
		}
	}
}
