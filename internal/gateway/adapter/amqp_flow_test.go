package adapter

import (
	"github.com/Someblueman/airc/internal/gateway/amqpwire"
	"testing"
)

func TestAMQPCreditSerialBoundaries(t *testing.T) {
	for _, tc := range []struct {
		count, ack, window, want uint32
		ok                       bool
	}{
		{1, 1, 8, 8, true}, {5, 1, 2, 0, true}, {1, 0xfffffffe, 8, 5, true}, {1, 2, 8, 0, false}, {1, 1, 1 << 31, 0, false},
	} {
		got, ok := amqpCredit(tc.count, tc.ack, tc.window)
		if got != tc.want || ok != tc.ok {
			t.Fatal(tc, got, ok)
		}
	}
}
func TestAMQPFlowEchoDrainAndRefusal(t *testing.T) {
	links := amqpLinkFixture()
	if _, c := links.attach(amqpAttach(false, 1, "response")); c != "" {
		t.Fatal(c)
	}
	s := newAMQPSession(amqpwire.Negotiation{NextIncoming: 7})
	p := amqpwire.Performative{Kind: 0x13, Fields: []amqpwire.Value{{Code: 0x70, Number: 1}, {Code: 0x70, Number: 8}, {Code: 0x70, Number: 7}, {Code: 0x70, Number: 8}, {Code: 0x70, Number: 1}, {Code: 0x70}, {Code: 0x70, Number: 9}, {Code: 0x40}, {Code: 0x56}, {Code: 0x56, Number: 1}}}
	reply, c := s.flow(p, &links)
	if c != "" || reply.Kind != 0x13 || reply.Fields[4].Number != 0 || links.links[0].credit != 9 || reply.Fields[6].Number != 9 {
		t.Fatal(reply, c, links.links[0])
	}
	p.Fields[8].Number = 1
	reply, c = s.flow(p, &links)
	if c != "" || reply.Fields[5].Number != 9 || reply.Fields[8].Number != 1 || links.links[0].credit != 0 {
		t.Fatal(reply, c)
	}
	before := s
	linkBefore := *links.links[0]
	p.Fields[4].Number = 99
	if _, c = s.flow(p, &links); c != "amqp:session:unattached-handle" || s != before || *links.links[0] != linkBefore {
		t.Fatal(c)
	}
	p.Fields = p.Fields[:4]
	p.Fields = append(p.Fields, amqpwire.Value{Code: 0x40}, amqpwire.Value{Code: 0x40}, amqpwire.Value{Code: 0x40}, amqpwire.Value{Code: 0x40}, amqpwire.Value{Code: 0x56})
	if _, c = s.flow(p, &links); c != "amqp:invalid-field" {
		t.Fatal(c)
	}
}
