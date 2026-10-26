package core

import (
	"strings"
	"testing"
)

func TestPinMutationNormalizeAndAcknowledgement(t *testing.T) {
	parent := strings.Repeat("a", 32)
	for _, op := range []Operation{PinsAdd, PinsRemove} {
		r := Request{Version: 1, RequestID: "pin", Operation: op, Target: &Target{Kind: "room", Name: "#room"}, ParentID: parent}
		if err := r.Normalize(); err != nil {
			t.Fatal(err)
		}
		ack := &PinAcknowledgement{Version: 1, Operation: op, Target: *r.Target, ParentID: parent}
		if _, err := PinAckBytes(ack, r); err != nil {
			t.Fatal(err)
		}
		ack.ParentID = strings.Repeat("b", 32)
		if _, err := PinAckBytes(ack, r); err == nil {
			t.Fatal("accepted mismatched pin parent")
		}
		ack.ParentID = parent
		r.Target = &Target{Kind: "room", Name: " #room "}
		ack.Target = *r.Target
		if _, err := PinAckBytes(ack, r); err == nil {
			t.Fatal("accepted acknowledgement for unnormalized private target")
		}
	}
}

func TestPinMutationRejectsInvalidAndForeignFields(t *testing.T) {
	for _, op := range []Operation{PinsAdd, PinsRemove} {
		for _, mutate := range []func(*Request){
			func(r *Request) { r.Target = &Target{Kind: "direct", Name: "alice"} },
			func(r *Request) { r.ParentID = "bad" },
			func(r *Request) { r.Message = "message" },
			func(r *Request) { r.Reaction = "agree" },
			func(r *Request) { r.Cursor = "cursor" },
		} {
			r := Request{Version: 1, RequestID: "pin", Operation: op, Target: &Target{Kind: "room", Name: "#room"}, ParentID: strings.Repeat("a", 32)}
			mutate(&r)
			if err := r.Normalize(); err == nil {
				t.Fatalf("accepted invalid %s mutation", op)
			}
		}
	}
}
