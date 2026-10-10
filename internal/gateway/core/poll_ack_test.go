package core

import (
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

func pollAckFixture(op Operation) (Request, *PollAcknowledgement) {
	id := strings.Repeat("a", 32)
	target := Target{Kind: "room", Name: "#room"}
	r := Request{Version: 1, RequestID: "poll-ack", Operation: op, Target: &target}
	if op == PollCreate {
		r.Poll = &PollInput{Question: "choose", Options: []string{"one", "two"}, TTLSeconds: 60}
	} else {
		r.PollID = id
		if op == PollVote {
			r.Choice = 2
		}
	}
	expires := time.Unix(1700000060, 0).UTC()
	poll := PollResult{Target: target, ID: id, From: "author", AccountID: strings.Repeat("b", 32), Question: "choose", Options: []string{"one", "two"}, Votes: []int{0, 0}, ExpiresAt: expires}
	ack := &PollAcknowledgement{Version: 1, Operation: op, Target: target, Action: "results", Poll: poll}
	if op == PollCreate {
		ack.Receipt = &irc.SendReceiptEvent{Type: "send_receipt", ChatMetadata: irc.ChatMetadata{Kind: "poll", AccountID: poll.AccountID, PollOptions: poll.Options, PollClosesAt: expires}, ID: id, From: "author", Target: "#room", Message: "choose", Timestamp: time.Unix(1700000000, 0).UTC(), Receipt: &irc.ReceiptInfo{Accepted: true}}
	}
	return r, ack
}

func TestPollAckBytesAllOperations(t *testing.T) {
	for _, op := range []Operation{PollCreate, PollVote, PollClose} {
		r, ack := pollAckFixture(op)
		if op == PollClose {
			ack.Poll.Closed = true
		}
		for _, persisted := range []bool{false, true} {
			ack.Receipt = nil
			if op == PollCreate {
				_, receipt := pollAckFixture(op)
				ack.Receipt = receipt.Receipt
				ack.Receipt.Receipt.Persisted = persisted
			}
			if op == PollCreate && ack.Receipt.Receipt.RecipientConnected != nil {
				t.Fatal("fixture unexpectedly has recipient connection")
			}
			if _, err := PollAckBytes(ack, r, "author"); err != nil {
				t.Fatalf("%s persisted=%t: %v", op, persisted, err)
			}
		}
	}
}

func TestPollAckRejectsMalformedFacts(t *testing.T) {
	r, _ := pollAckFixture(PollCreate)
	for name, mutate := range map[string]func(*PollAcknowledgement){
		"missing receipt":        func(a *PollAcknowledgement) { a.Receipt = nil },
		"retracted receipt":      func(a *PollAcknowledgement) { a.Receipt.Retracted = true },
		"queued receipt":         func(a *PollAcknowledgement) { a.Receipt.Queued = true },
		"foreign receipt fields": func(a *PollAcknowledgement) { a.Receipt.ReplyTo = strings.Repeat("c", 32) },
		"recipient connected":    func(a *PollAcknowledgement) { v := true; a.Receipt.Receipt.RecipientConnected = &v },
		"wrong option facts":     func(a *PollAcknowledgement) { a.Poll.Options[0] = "other" },
		"new poll votes":         func(a *PollAcknowledgement) { a.Poll.Votes[0] = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			_, a := pollAckFixture(PollCreate)
			mutate(a)
			if _, err := PollAckBytes(a, r, "author"); err == nil {
				t.Fatal("accepted malformed poll acknowledgement")
			}
		})
	}
}

func TestPollAckRejectsForeignOperationsAndCloseState(t *testing.T) {
	r, ack := pollAckFixture(PollVote)
	ack.Operation = PollClose
	if _, err := PollAckBytes(ack, r, "different"); err == nil {
		t.Fatal("accepted mismatched operation")
	}
	r, ack = pollAckFixture(PollClose)
	if _, err := PollAckBytes(ack, r, "different"); err == nil {
		t.Fatal("accepted unclosed poll")
	}
	r, ack = pollAckFixture(PollVote)
	if _, err := PollAckBytes(ack, r, "different"); err != nil {
		t.Fatal("rejected vote result whose From is the poll author", err)
	}
}

func TestPollRequestRejectsInvalidNativeBounds(t *testing.T) {
	base := Request{Version: 1, RequestID: "poll", Operation: PollCreate, Target: &Target{Kind: "room", Name: "#room"}, Poll: &PollInput{Question: "q", Options: []string{"a", "b"}, TTLSeconds: 1}}
	for name, mutate := range map[string]func(*Request){
		"zero ttl":         func(r *Request) { r.Poll.TTLSeconds = 0 },
		"long question":    func(r *Request) { r.Poll.Question = strings.Repeat("x", 1001) },
		"control question": func(r *Request) { r.Poll.Question = "\nquestion" },
		"duplicate option": func(r *Request) { r.Poll.Options[1] = r.Poll.Options[0] },
		"too many options": func(r *Request) { r.Poll.Options = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} },
	} {
		t.Run(name, func(t *testing.T) {
			r := base
			p := *base.Poll
			p.Options = append([]string(nil), base.Poll.Options...)
			r.Poll = &p
			mutate(&r)
			if err := r.Normalize(); err == nil {
				t.Fatal("accepted invalid poll request")
			}
		})
	}
}

func TestPollVoteRequestRejectsImpossibleChoice(t *testing.T) {
	r, _ := pollAckFixture(PollVote)
	r.Choice = 9
	if err := r.Normalize(); err == nil {
		t.Fatal("accepted choice greater than native maximum options")
	}
}
