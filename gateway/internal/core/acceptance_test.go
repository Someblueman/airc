package core

import (
	"errors"
	"reflect"
	"testing"

	"github.com/Someblueman/airc/pkg/irc"
)

// TestAcceptanceDescriptors checks the descriptor table's structure. Each
// kind's correlation rules are tested with its validator, and every
// operation's evidence end to end in store.TestAcceptance.
func TestAcceptanceDescriptors(t *testing.T) {
	one := map[evidenceKind]Acceptance{
		receiptKind:     {Receipt: &irc.SendReceiptEvent{}},
		cardKind:        {Card: &CardAcknowledgement{}},
		topicKind:       {Topic: &TopicAcknowledgement{}},
		roomKind:        {Room: &RoomAcknowledgement{}},
		pinKind:         {Pin: &PinAcknowledgement{}},
		activityKind:    {Activity: &ActivityAcknowledgement{}},
		replySignalKind: {ReplySignal: &ReplySignalAcknowledgement{}},
		pollKind:        {Poll: &PollAcknowledgement{}},
	}
	if len(one) != len(evidences) || len(evidences) != reflect.TypeFor[Acceptance]().NumField() {
		t.Fatal("an Acceptance field has no evidence kind")
	}
	used := map[evidenceKind]bool{}
	for op, m := range mutations {
		if !op.IsMutation() || op.HasLookup() != (op == Send || op == Reply || op == ReactionAdd) {
			t.Fatalf("%s: mutation or lookup flag", op)
		}
		used[m.evidence] = true
		for kind, a := range one {
			if _, err := a.Encode(Request{Operation: op}, "", "author"); kind != m.evidence && !errors.Is(err, errAcceptance) {
				t.Fatalf("%s accepted evidence kind %d: %v", op, kind, err)
			}
			if a.fields() != 1 || !evidences[kind].set(a) {
				t.Fatalf("kind %d does not name its own field", kind)
			}
		}
		decoded, err := DecodeAcceptance(op, []byte("{}"))
		if err != nil || decoded.fields() != 1 || !evidences[m.evidence].set(decoded) {
			t.Fatalf("%s decoded %+v: %v", op, decoded, err)
		}
	}
	if len(used) != len(evidences) {
		t.Fatal("an evidence kind no operation produces")
	}
	two := Acceptance{Receipt: &irc.SendReceiptEvent{}, Card: &CardAcknowledgement{}}
	for _, a := range []Acceptance{{}, two} {
		if _, err := a.Encode(Request{Operation: Send}, "", "author"); !errors.Is(err, errAcceptance) {
			t.Fatalf("encoded %+v: %v", a, err)
		}
	}
	if _, err := one[receiptKind].Encode(Request{Operation: HistoryRead}, "", "author"); !errors.Is(err, errAcceptance) || HistoryRead.IsMutation() || HistoryRead.HasLookup() {
		t.Fatal("a read has acceptance evidence", err)
	}
	if _, err := DecodeAcceptance(HistoryRead, []byte("{}")); err == nil {
		t.Fatal("decoded evidence for a read")
	}
	receipt := &irc.SendReceiptEvent{ID: "x"}
	for _, c := range []struct {
		a    Acceptance
		want *irc.SendReceiptEvent
	}{
		{Acceptance{Receipt: receipt}, receipt},
		{Acceptance{Poll: &PollAcknowledgement{Operation: PollCreate, Receipt: receipt}}, receipt},
		{Acceptance{Poll: &PollAcknowledgement{Operation: PollVote, Receipt: receipt}}, nil},
		{Acceptance{Card: &CardAcknowledgement{}}, nil},
	} {
		if got := c.a.MessageReceipt(); got != c.want {
			t.Fatalf("message receipt of %+v: %v", c.a, got)
		}
	}
}
