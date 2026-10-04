package core

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Someblueman/airc/pkg/irc"
)

const MaxCardAcknowledgementBytes = 16 << 10
const MaxProfilePatchBytes = 4096

// ProfilePatch changes only the explicitly supplied fields of the caller's
// public card. An explicit empty string clears one field.
type ProfilePatch struct {
	Model     *string `json:"model,omitempty"`
	Workspace *string `json:"workspace,omitempty"`
	Tools     *string `json:"tools,omitempty"`
	About     *string `json:"about,omitempty"`
	Clear     bool    `json:"clear,omitempty"`
}

type PresenceInput struct {
	State      string `json:"state"`
	Note       string `json:"note,omitempty"`
	TTLSeconds int64  `json:"ttl_seconds"`
}

// CardAcknowledgement is the bounded, authenticated upstream card observed
// after a profile or presence mutation.
type CardAcknowledgement struct {
	Version   int           `json:"version"`
	Operation Operation     `json:"operation"`
	Card      irc.AgentCard `json:"card"`
}

func cardText(s string, max int) bool {
	return len(s) <= max && utf8.ValidString(s) && strings.IndexFunc(s, unicode.IsControl) < 0
}

func (p *ProfilePatch) valid() bool {
	if p == nil || p.Clear && (p.Model != nil || p.Workspace != nil || p.Tools != nil || p.About != nil) {
		return false
	}
	for _, field := range []*string{p.Model, p.Workspace, p.Tools, p.About} {
		if field != nil && !cardText(*field, 400) {
			return false
		}
	}
	data, err := json.Marshal(p)
	if err != nil || len(data) > MaxProfilePatchBytes {
		return false
	}
	return p.Clear || p.Model != nil || p.Workspace != nil || p.Tools != nil || p.About != nil
}

func (p *PresenceInput) valid() bool {
	if p == nil || !utf8.ValidString(p.Note) || !cardText(p.Note, 240) || strings.TrimSpace(p.Note) != p.Note {
		return false
	}
	if p.State == "clear" {
		return p.TTLSeconds == 0 && p.Note == ""
	}
	return p.TTLSeconds >= 1 && p.TTLSeconds <= 3600 && (p.State == "available" || p.State == "thinking" || p.State == "running" || p.State == "away")
}

func validCardID(s string) bool {
	if len(s) != 32 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func validCardState(s string) bool {
	switch s {
	case "unknown", "available", "thinking", "running", "away":
		return true
	default:
		return false
	}
}

// CardAckBytes checks that upstream evidence belongs to the requested
// self-mutation and remains within the transport's bounded receipt budget.
func CardAckBytes(ack *CardAcknowledgement, request Request, author string) ([]byte, error) {
	if ack == nil || ack.Version != 1 || ack.Operation != request.Operation || request.Target != nil || author == "" || request.Operation != ProfileUpdate && request.Operation != PresenceSet {
		return nil, errors.New("invalid card acknowledgement")
	}
	if err := (&Target{Kind: "direct", Name: author}).Normalize(); err != nil || ack.Card.Nick != author || !validCardID(ack.Card.AccountID) || ack.Card.LastSeen.IsZero() {
		return nil, errors.New("invalid card identity")
	}
	if !validCardState(ack.Card.State) || !cardText(ack.Card.Model, 400) || !cardText(ack.Card.Workspace, 400) || !cardText(ack.Card.Tools, 400) || !cardText(ack.Card.About, 400) || !cardText(ack.Card.Note, 240) {
		return nil, errors.New("invalid card fields")
	}
	if ack.Operation == ProfileUpdate {
		if request.Profile == nil || !request.Profile.valid() || ack.Card.State == "" {
			return nil, errors.New("invalid profile acknowledgement")
		}
		p := request.Profile
		if p.Clear && (ack.Card.Model != "" || ack.Card.Workspace != "" || ack.Card.Tools != "" || ack.Card.About != "") {
			return nil, errors.New("profile clear not acknowledged")
		}
		for field, want := range map[string]*string{"model": p.Model, "workspace": p.Workspace, "tools": p.Tools, "about": p.About} {
			if want == nil {
				continue
			}
			var got string
			switch field {
			case "model":
				got = ack.Card.Model
			case "workspace":
				got = ack.Card.Workspace
			case "tools":
				got = ack.Card.Tools
			case "about":
				got = ack.Card.About
			}
			if got != *want {
				return nil, errors.New("profile field mismatch")
			}
		}
	} else {
		if request.Presence == nil || !request.Presence.valid() {
			return nil, errors.New("invalid presence acknowledgement")
		}
		wantState := request.Presence.State
		if wantState == "clear" {
			wantState = "unknown"
		}
		if ack.Card.State != wantState || ack.Card.Note != request.Presence.Note {
			return nil, errors.New("invalid presence acknowledgement")
		}
		if request.Presence.TTLSeconds == 0 {
			if !ack.Card.ExpiresAt.IsZero() {
				return nil, errors.New("presence clear expiry")
			}
		} else if ack.Card.ExpiresAt.IsZero() || ack.Card.ExpiresAt.Sub(ack.Card.LastSeen) != time.Duration(request.Presence.TTLSeconds)*time.Second {
			return nil, errors.New("presence expiry missing")
		}
	}
	data, err := json.Marshal(ack)
	if err != nil || len(data) > MaxCardAcknowledgementBytes {
		return nil, errors.New("invalid card acknowledgement")
	}
	return data, nil
}
