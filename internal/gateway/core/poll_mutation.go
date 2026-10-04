package core

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

const (
	PollCreate Operation = "poll.create"
	PollVote   Operation = "poll.vote"
	PollClose  Operation = "poll.close"
)

type PollInput struct {
	Question   string   `json:"question"`
	Options    []string `json:"options"`
	TTLSeconds int64    `json:"ttl_seconds"`
}

type PollAcknowledgement struct {
	Version   int                   `json:"version"`
	Operation Operation             `json:"operation"`
	Target    Target                `json:"target"`
	Action    string                `json:"action"`
	Poll      PollResult            `json:"poll"`
	Receipt   *irc.SendReceiptEvent `json:"receipt,omitempty"`
}

const MaxPollAckBytes = 16 << 10

func (p *PollInput) valid() bool {
	if p == nil || strings.TrimSpace(p.Question) == "" || len(p.Options) < 2 || len(p.Options) > 8 || p.TTLSeconds < 1 || p.TTLSeconds > 7*24*3600 {
		return false
	}
	if !protocol.BriefText(p.Question, 1000) || len(p.Question) > 1000 {
		return false
	}
	seen := make(map[string]bool, len(p.Options))
	for _, option := range p.Options {
		if option == "" || !protocol.BriefText(option, 80) || seen[option] {
			return false
		}
		seen[option] = true
	}
	return true
}

func validPollAccountID(s string) bool {
	if s == "" {
		return true
	}
	if len(s) != 32 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func PollAckBytes(ack *PollAcknowledgement, request Request, author string) ([]byte, error) {
	if ack == nil || ack.Version != 1 || ack.Action != "results" || request.Operation != ack.Operation || (ack.Operation != PollCreate && ack.Operation != PollVote && ack.Operation != PollClose) || request.Target == nil || request.Target.Kind != "room" {
		return nil, errors.New("invalid poll acknowledgement")
	}
	originalTarget := *request.Target
	normalized := request
	target := originalTarget
	normalized.Target = &target
	if request.Poll != nil {
		input := *request.Poll
		input.Options = append([]string(nil), request.Poll.Options...)
		normalized.Poll = &input
	}
	if err := normalized.Normalize(); err != nil || originalTarget != *normalized.Target || ack.Target != *normalized.Target || ack.Poll.Target != *normalized.Target || !validPollID(ack.Poll.ID) || !validPollAccountID(ack.Poll.AccountID) || !validPollAuthor(ack.Poll.From) || ack.Poll.ExpiresAt.IsZero() || len(ack.Poll.Options) < 2 || len(ack.Poll.Options) > 8 || len(ack.Poll.Votes) != len(ack.Poll.Options) || !protocol.BriefText(ack.Poll.Question, 1000) || strings.TrimSpace(ack.Poll.Question) == "" {
		return nil, errors.New("invalid poll acknowledgement")
	}
	request = normalized
	if request.Operation == PollCreate {
		if request.Poll == nil || request.Poll.Question != ack.Poll.Question || len(request.Poll.Options) != len(ack.Poll.Options) || !slices.Equal(request.Poll.Options, ack.Poll.Options) || ack.Receipt == nil || ack.Receipt.Type != "send_receipt" || ack.Receipt.Receipt == nil || !ack.Receipt.Receipt.Accepted || ack.Receipt.Receipt.RecipientConnected != nil || ack.Receipt.ID != ack.Poll.ID || ack.Receipt.RequestID != "" || ack.Receipt.Kind != "poll" || ack.Receipt.Message != ack.Poll.Question || ack.Receipt.Target != ack.Poll.Target.Name || !slices.Equal(ack.Receipt.PollOptions, ack.Poll.Options) || !ack.Receipt.PollClosesAt.Equal(ack.Poll.ExpiresAt) || ack.Receipt.From != ack.Poll.From || ack.Receipt.AccountID == "" || ack.Receipt.AccountID != ack.Poll.AccountID || ack.Receipt.ReplyTo != "" || ack.Receipt.Reaction != "" || ack.Receipt.Retracted || ack.Receipt.Supersedes != "" || ack.Receipt.SupersededBy != "" || ack.Receipt.ThreadID != "" && ack.Receipt.ThreadID != ack.Receipt.ID || ack.Receipt.Queued || ack.Receipt.Timestamp.IsZero() || !validPollAuthor(author) || ack.Poll.From != author {
			return nil, errors.New("invalid poll creation acknowledgement")
		}
	} else {
		if request.Poll != nil || ack.Receipt != nil || request.PollID != ack.Poll.ID {
			return nil, errors.New("invalid poll mutation acknowledgement")
		}
	}
	if request.Operation == PollVote && (request.Choice < 1 || request.Choice > len(ack.Poll.Options) || request.Choice > 8) || request.Operation != PollVote && request.Choice != 0 {
		return nil, errors.New("invalid poll choice")
	}
	if request.Operation == PollClose && !ack.Poll.Closed {
		return nil, errors.New("closed poll acknowledgement is not closed")
	}
	total := 0
	seen := make(map[string]bool, len(ack.Poll.Options))
	for i, option := range ack.Poll.Options {
		if option == "" || !protocol.BriefText(option, 80) || seen[option] || ack.Poll.Votes[i] < 0 || ack.Poll.Votes[i] > 256-total {
			return nil, errors.New("invalid poll result")
		}
		seen[option] = true
		total += ack.Poll.Votes[i]
	}
	if request.Operation == PollCreate {
		for _, vote := range ack.Poll.Votes {
			if vote != 0 {
				return nil, errors.New("new poll has votes")
			}
		}
	}
	if _, err := ack.Poll.ExpiresAt.MarshalJSON(); err != nil {
		return nil, errors.New("invalid poll expiry")
	}
	data, err := json.Marshal(ack)
	if err != nil || len(data) > MaxPollAckBytes {
		return nil, errors.New("invalid poll acknowledgement")
	}
	return data, nil
}

func validPollID(s string) bool { return protocol.ValidMessageID(s) }
func validPollAuthor(s string) bool {
	if s == "" || strings.TrimSpace(s) != s {
		return false
	}
	return (&Target{Kind: "direct", Name: s}).Normalize() == nil
}
