package adapter

import (
	"github.com/Someblueman/airc/internal/gateway/amqpwire"
	"testing"
	"time"
)

func TestAMQPSettlementRangesAndDirection(t *testing.T) {
	for _, kind := range []string{"receiver", "sender", "unsettled", "unknown", "wrap", "ambiguous", "sender_state"} {
		t.Run(kind, func(t *testing.T) {
			in := &amqpLink{incoming: true, inbound: &amqpIncoming{id: 1}}
			out := &amqpLink{outbound: &amqpOutgoing{id: 1, offset: 1}}
			links := amqpLinks{links: [2]*amqpLink{in, out}}
			p := amqpwire.Performative{Kind: 0x15, Fields: []amqpwire.Value{{Code: 0x56, Number: 1}, {Code: 0x70, Number: 1}, {Code: 0x40}, {Code: 0x56, Number: 1}}}
			want := ""
			switch kind {
			case "sender":
				p.Fields[0].Number = 0
			case "unsettled":
				p.Fields[3].Number = 0
			case "unknown":
				p.Fields[1].Number = 2
			case "wrap":
				p.Fields[1].Number = 0xffffffff
				p.Fields[2] = amqpwire.Value{Code: 0x70, Number: 1}
			case "ambiguous":
				p.Fields[2] = amqpwire.Value{Code: 0x70, Number: 0x80000001}
				want = "amqp:invalid-field"
			case "sender_state":
				p.Fields[0].Number = 0
				p.Fields = append(p.Fields, amqpOutcome(0x24, ""))
				want = "amqp:not-implemented"
			}
			if got := links.disposition(p); got != want {
				t.Fatal(got, want)
			}
			if out.outbound.settled != (kind == "receiver" || kind == "wrap") || in.inbound.settled != (kind == "sender") {
				t.Fatal(in.inbound, out.outbound)
			}
		})
	}
}

func TestAMQPEarlyReceiverSettlementRetainsRemainingFragments(t *testing.T) {
	now := time.Now()
	s := amqpSession{outgoingWindow: 1, remoteIncoming: 1}
	l := &amqpLink{messageLimit: 65536, credit: 1}
	links := amqpLinks{links: [2]*amqpLink{l, nil}}
	if condition := s.queueResponse(l, make([]byte, 1500), []byte("tag"), now); condition != "" {
		t.Fatal(condition)
	}
	write := func(amqpwire.Performative, time.Time) error { return nil }
	if err := s.flushResponse(l, 512, now, write); err != nil {
		t.Fatal(err)
	}
	offset, deadline := l.outbound.offset, l.outbound.deadline
	if offset == 0 || l.outbound.complete {
		t.Fatal(l.outbound)
	}
	if condition := links.disposition(amqpwire.Performative{Kind: 0x15, Fields: []amqpwire.Value{{Code: 0x56, Number: 1}, {Code: 0x70}, {Code: 0x40}, {Code: 0x56, Number: 1}}}); condition != "" {
		t.Fatal(condition)
	}
	if !l.outbound.settled || l.outbound.offset != offset || l.outbound.deadline != deadline || len(l.outbound.encoded) != 1500 {
		t.Fatal("early settlement discarded remaining data")
	}
	s.outgoingWindow, s.remoteIncoming = 8, 8
	if err := s.flushResponse(l, 512, now, write); err != nil || !l.outbound.complete || l.outbound.offset != 1500 {
		t.Fatal(l.outbound, err)
	}
}
