package core

import (
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

func TestProfileAndPresenceNormalize(t *testing.T) {
	model := "model"
	profile := Request{Version: 1, RequestID: "profile", Operation: ProfileUpdate, Profile: &ProfilePatch{Model: &model}}
	if err := profile.Normalize(); err != nil {
		t.Fatal(err)
	}
	presence := Request{Version: 1, RequestID: "presence", Operation: PresenceSet, Presence: &PresenceInput{State: "thinking", TTLSeconds: 60}}
	if err := presence.Normalize(); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []Request{
		{Version: 1, RequestID: "p", Operation: ProfileUpdate, Profile: &ProfilePatch{}},
		{Version: 1, RequestID: "p", Operation: ProfileUpdate, Profile: &ProfilePatch{Clear: true, Model: &model}},
		{Version: 1, RequestID: "p", Operation: PresenceSet, Presence: &PresenceInput{State: "thinking", TTLSeconds: 0}},
		{Version: 1, RequestID: "p", Operation: PresenceSet, Presence: &PresenceInput{State: "clear", Note: "x"}},
	} {
		if err := invalid.Normalize(); err == nil {
			t.Fatalf("accepted invalid %s", invalid.Operation)
		}
	}
	tooLong := strings.Repeat("x", 401)
	tooLongRequest := Request{Version: 1, RequestID: "p", Operation: ProfileUpdate, Profile: &ProfilePatch{About: &tooLong}}
	if err := tooLongRequest.Normalize(); err == nil {
		t.Fatal("accepted oversized profile field")
	}
}

func TestProfileFieldsDoNotChangeOldFingerprint(t *testing.T) {
	r := Request{Version: 1, RequestID: "one", Operation: Send, Target: &Target{Kind: "direct", Name: "Alice"}, Message: "a\r\nb"}
	fingerprint, err := r.Fingerprint()
	if err != nil || fingerprint != "c84fc5674d0c0b6c3e88effa8ea9ca890b2096744866761b588233ebea1a716e" {
		t.Fatalf("published fingerprint changed: %s %v", fingerprint, err)
	}
}

func TestProfilePatchAndCardBounds(t *testing.T) {
	large := strings.Repeat("<", 400)
	patch := &ProfilePatch{Model: &large, Workspace: &large, Tools: &large, About: &large}
	r := Request{Version: 1, RequestID: "large", Operation: ProfileUpdate, Profile: patch}
	if err := r.Normalize(); err == nil {
		t.Fatal("accepted oversized encoded profile patch")
	}
	ack := &CardAcknowledgement{Version: 1, Operation: ProfileUpdate, Card: irc.AgentCard{Nick: "agent", AccountID: strings.Repeat("a", 32), Model: large, Workspace: large, Tools: large, About: large, State: "unknown", LastSeen: time.Unix(100, 0)}}
	if _, err := CardAckBytes(ack, r, "agent"); err == nil {
		t.Fatal("expected malformed request to fail")
	}
	if err := r.Normalize(); err == nil {
		t.Fatal("oversized patch unexpectedly normalized")
	}
	// A native card can contain all four bounded fields; its escaped JSON is
	// larger than 4 KiB but remains within the acknowledgement budget.
	model := large
	valid := Request{Version: 1, RequestID: "card", Operation: ProfileUpdate, Profile: &ProfilePatch{Model: &model}}
	if err := valid.Normalize(); err != nil {
		t.Fatal(err)
	}
	card := &CardAcknowledgement{Version: 1, Operation: ProfileUpdate, Card: irc.AgentCard{Nick: "agent", AccountID: strings.Repeat("a", 32), Model: large, Workspace: large, Tools: large, About: large, State: "unknown", LastSeen: time.Unix(100, 0)}}
	data, err := CardAckBytes(card, valid, "agent")
	if err != nil || len(data) <= 4096 || len(data) > MaxCardAcknowledgementBytes {
		t.Fatalf("card acknowledgement bounds: len=%d err=%v", len(data), err)
	}
}

func TestProfilePresenceRejectForeignSelectors(t *testing.T) {
	model := "m"
	for _, request := range []Request{
		{Version: 1, RequestID: "p", Operation: ProfileUpdate, Target: &Target{Kind: "room", Name: "#room"}, Profile: &ProfilePatch{Model: &model}},
		{Version: 1, RequestID: "p", Operation: PresenceSet, ParentID: strings.Repeat("a", 32), Presence: &PresenceInput{State: "away", TTLSeconds: 1}},
		{Version: 1, RequestID: "p", Operation: Send, Target: &Target{Kind: "room", Name: "#room"}, Message: "x", Profile: &ProfilePatch{Model: &model}},
	} {
		if err := request.Normalize(); err == nil {
			t.Fatalf("accepted foreign selector/field for %s", request.Operation)
		}
	}
}

func TestCardAcknowledgement(t *testing.T) {
	model := "model"
	req := Request{Version: 1, RequestID: "profile", Operation: ProfileUpdate, Profile: &ProfilePatch{Model: &model}}
	ack := &CardAcknowledgement{Version: 1, Operation: ProfileUpdate, Card: irc.AgentCard{Nick: "agent", AccountID: strings.Repeat("a", 32), State: "unknown", Model: model, LastSeen: time.Unix(100, 0)}}
	if _, err := CardAckBytes(ack, req, "agent"); err != nil {
		t.Fatal(err)
	}
	ack.Card.Model = "other"
	if _, err := CardAckBytes(ack, req, "agent"); err == nil {
		t.Fatal("accepted mismatched profile acknowledgement")
	}
	ack.Card.Model = model
	ack.Card.Nick = "Agent"
	if _, err := CardAckBytes(ack, req, "agent"); err == nil {
		t.Fatal("accepted nickname case mismatch")
	}
	ack.Card.Nick = "agent"
	if _, err := CardAckBytes(ack, req, "agent"); err != nil {
		t.Fatal(err)
	}
	ack.Card.AccountID = ""
	if _, err := CardAckBytes(ack, req, "agent"); err == nil {
		t.Fatal("accepted missing account identity")
	}
	when := time.Unix(100, 0)
	presence := Request{Version: 1, RequestID: "presence", Operation: PresenceSet, Presence: &PresenceInput{State: "away", Note: "busy", TTLSeconds: 60}}
	presenceAck := &CardAcknowledgement{Version: 1, Operation: PresenceSet, Card: irc.AgentCard{Nick: "agent", AccountID: strings.Repeat("b", 32), State: "away", Note: "busy", ExpiresAt: when.Add(60 * time.Second), LastSeen: when}}
	if _, err := CardAckBytes(presenceAck, presence, "agent"); err != nil {
		t.Fatal(err)
	}
	presenceAck.Card.ExpiresAt = presenceAck.Card.ExpiresAt.Add(time.Second)
	if _, err := CardAckBytes(presenceAck, presence, "agent"); err == nil {
		t.Fatal("accepted wrong presence expiry delta")
	}
}
