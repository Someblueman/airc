package core

import (
	"encoding/json"
	"errors"

	"github.com/Someblueman/airc/pkg/irc"
)

var errAcceptance = errors.New("invalid acceptance")

// Acceptance is AIRC's acknowledgement of one mutation, the evidence a job
// saves as its outcome. Exactly one field is set: the one the operation's
// descriptor in mutations names.
type Acceptance struct {
	Receipt     *irc.SendReceiptEvent
	Card        *CardAcknowledgement
	Topic       *TopicAcknowledgement
	Room        *RoomAcknowledgement
	Pin         *PinAcknowledgement
	Activity    *ActivityAcknowledgement
	ReplySignal *ReplySignalAcknowledgement
	Poll        *PollAcknowledgement
}

// evidence is one kind of acceptance: its field in Acceptance and the check
// that binds it to the stored intent and encodes it for the journal.
type evidence struct {
	set    func(Acceptance) bool
	encode func(a Acceptance, request Request, upstreamID, author string) ([]byte, error)
	decode func(data []byte, a *Acceptance) error
}

func kind[T any](field func(*Acceptance) **T, encode func(*T, Request, string, string) ([]byte, error)) evidence {
	return evidence{
		set: func(a Acceptance) bool { return *field(&a) != nil },
		encode: func(a Acceptance, r Request, upstreamID, author string) ([]byte, error) {
			return encode(*field(&a), r, upstreamID, author)
		},
		decode: func(data []byte, a *Acceptance) error {
			v := new(T)
			if err := json.Unmarshal(data, v); err != nil {
				return err
			}
			*field(a) = v
			return nil
		},
	}
}

func withAuthor[T any](f func(*T, Request, string) ([]byte, error)) func(*T, Request, string, string) ([]byte, error) {
	return func(v *T, r Request, _, author string) ([]byte, error) { return f(v, r, author) }
}

func withRequest[T any](f func(*T, Request) ([]byte, error)) func(*T, Request, string, string) ([]byte, error) {
	return func(v *T, r Request, _, _ string) ([]byte, error) { return f(v, r) }
}

// evidenceKind names one field of Acceptance.
type evidenceKind uint8

const (
	receiptKind evidenceKind = iota
	cardKind
	topicKind
	roomKind
	pinKind
	activityKind
	replySignalKind
	pollKind
)

var evidences = [...]evidence{
	receiptKind:     kind(func(a *Acceptance) **irc.SendReceiptEvent { return &a.Receipt }, ReceiptBytes),
	cardKind:        kind(func(a *Acceptance) **CardAcknowledgement { return &a.Card }, withAuthor(CardAckBytes)),
	topicKind:       kind(func(a *Acceptance) **TopicAcknowledgement { return &a.Topic }, withAuthor(TopicAckBytes)),
	roomKind:        kind(func(a *Acceptance) **RoomAcknowledgement { return &a.Room }, withRequest(RoomAckBytes)),
	pinKind:         kind(func(a *Acceptance) **PinAcknowledgement { return &a.Pin }, withRequest(PinAckBytes)),
	activityKind:    kind(func(a *Acceptance) **ActivityAcknowledgement { return &a.Activity }, withAuthor(ActivityAckBytes)),
	replySignalKind: kind(func(a *Acceptance) **ReplySignalAcknowledgement { return &a.ReplySignal }, withAuthor(ReplySignalAckBytes)),
	pollKind:        kind(func(a *Acceptance) **PollAcknowledgement { return &a.Poll }, withAuthor(PollAckBytes)),
}

// mutation describes one operation admitted as a durable job.
type mutation struct {
	evidence evidenceKind
	// lookup grants the job lookup attempts: AIRC can be asked by request ID
	// whether an uncertain send landed, and the answer is a receipt.
	lookup bool
}

// mutations is every durable operation, with the evidence of its acceptance.
var mutations = map[Operation]mutation{
	Send:              {evidence: receiptKind, lookup: true},
	Reply:             {evidence: receiptKind, lookup: true},
	ReactionAdd:       {evidence: receiptKind, lookup: true},
	MessageAction:     {evidence: receiptKind},
	MessageCorrect:    {evidence: receiptKind},
	MessageRetract:    {evidence: receiptKind},
	ProfileUpdate:     {evidence: cardKind},
	PresenceSet:       {evidence: cardKind},
	TopicSet:          {evidence: topicKind},
	RoomUpdate:        {evidence: roomKind},
	PinsAdd:           {evidence: pinKind},
	PinsRemove:        {evidence: pinKind},
	ActivitySet:       {evidence: activityKind},
	ReplySignalSet:    {evidence: replySignalKind},
	ReplySignalCancel: {evidence: replySignalKind},
	PollCreate:        {evidence: pollKind},
	PollVote:          {evidence: pollKind},
	PollClose:         {evidence: pollKind},
}

// HasLookup reports whether a job of this operation gets lookup attempts.
func (op Operation) HasLookup() bool { return mutations[op].lookup }

func (a Acceptance) fields() int {
	n := 0
	for _, set := range [...]bool{a.Receipt != nil, a.Card != nil, a.Topic != nil, a.Room != nil, a.Pin != nil, a.Activity != nil, a.ReplySignal != nil, a.Poll != nil} {
		if set {
			n++
		}
	}
	return n
}

// Encode checks that a is the acceptance of request, the stored intent, for
// the author whose session sent it, and returns the journal's copy of it.
// upstreamID is the request ID sent to AIRC; only a receipt carries it.
func (a Acceptance) Encode(request Request, upstreamID, author string) ([]byte, error) {
	m, ok := mutations[request.Operation]
	if !ok || a.fields() != 1 || !evidences[m.evidence].set(a) {
		return nil, errAcceptance
	}
	return evidences[m.evidence].encode(a, request, upstreamID, author)
}

// DecodeAcceptance reads back what Encode returned for an operation.
func DecodeAcceptance(op Operation, data []byte) (Acceptance, error) {
	var a Acceptance
	m, ok := mutations[op]
	if !ok {
		return a, errAcceptance
	}
	err := evidences[m.evidence].decode(data, &a)
	return a, err
}

// MessageReceipt is the receipt of the message an acceptance posted, if any:
// a message's own receipt, or a new poll's.
func (a Acceptance) MessageReceipt() *irc.SendReceiptEvent {
	if a.Poll != nil && a.Poll.Operation == PollCreate {
		return a.Poll.Receipt
	}
	return a.Receipt
}
