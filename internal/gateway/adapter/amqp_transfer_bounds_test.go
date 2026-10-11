package adapter

import (
	"bytes"
	"github.com/Someblueman/airc/internal/gateway/amqpwire"
	"testing"
	"time"
)

func TestAMQPIncomingProfileRefusalsDoNotMutate(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*amqpwire.Performative)
		want string
	}{
		{"missing id", func(p *amqpwire.Performative) { p.Fields[1] = amqpwire.Value{Code: 0x40} }, "amqp:invalid-field"},
		{"missing tag", func(p *amqpwire.Performative) { p.Fields[2] = amqpwire.Value{Code: 0x40} }, "amqp:invalid-field"},
		{"missing format", func(p *amqpwire.Performative) { p.Fields[3] = amqpwire.Value{Code: 0x40} }, "amqp:invalid-field"},
		{"second settlement", func(p *amqpwire.Performative) { p.Fields = append(p.Fields, amqpwire.Value{Code: 0x50, Number: 1}) }, "amqp:not-implemented"},
		{"resume", func(p *amqpwire.Performative) {
			p.Fields = append(p.Fields, amqpwire.Value{Code: 0x40}, amqpwire.Value{Code: 0x40}, amqpwire.Value{Code: 0x56, Number: 1})
		}, "amqp:not-implemented"},
		{"state", func(p *amqpwire.Performative) {
			p.Fields = append(p.Fields, amqpwire.Value{Code: 0x40}, amqpwire.Value{Descriptor: &amqpwire.Value{Code: 0x80, Number: 0x24}, Children: []amqpwire.Value{{Code: 0xc0}}})
		}, "amqp:not-implemented"},
		{"unknown handle", func(p *amqpwire.Performative) { p.Fields[0].Number = 99 }, "amqp:session:unattached-handle"},
		{"receiver handle", func(p *amqpwire.Performative) { p.Fields[0].Number = 0 }, "amqp:invalid-field"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, links := amqpTransferFixture(t)
			l := links.links[0]
			l.credit = 1
			p := amqpTransfer(1, nil, true)
			tc.edit(&p)
			before := s
			old := *l
			if c := s.receive(p, &links, time.Now()); c != tc.want || s != before || *l != old {
				t.Fatal(c, s, l)
			}
		})
	}
	for _, index := range []int{1, 2} {
		t.Run("continuation", func(t *testing.T) {
			s, links := amqpTransferFixture(t)
			l := links.links[0]
			l.credit = 1
			now := time.Now()
			if c := s.receive(amqpTransfer(1, []byte("start"), true), &links, now); c != "" {
				t.Fatal(c)
			}
			p := amqpTransfer(1, []byte("end"), false)
			if index == 1 {
				p.Fields[1].Number = 2
			} else {
				p.Fields[2].Data = []byte("different")
			}
			before := s
			if c := s.receive(p, &links, now); c != "amqp:invalid-field" || s != before || string(l.inbound.encoded) != "start" || l.inbound.frames != 1 {
				t.Fatal(c)
			}
		})
	}
}
func TestAMQPIncomingEncodedByteWindowAndFrameLimits(t *testing.T) {
	s, links := amqpTransferFixture(t)
	l := links.links[0]
	now := time.Now()
	l.credit = 1
	if c := s.receive(amqpTransfer(5, bytes.Repeat([]byte{1}, 32768), true), &links, now); c != "" {
		t.Fatal(c)
	}
	if c := s.receive(amqpContinuation(bytes.Repeat([]byte{2}, 32768), true), &links, now); c != "" {
		t.Fatal(c)
	}
	before := s
	if c := s.receive(amqpContinuation([]byte{3}, false), &links, now); c != "amqp:link:message-size-exceeded" || s != before || len(l.inbound.encoded) != 65536 {
		t.Fatal(c)
	}
	if c := s.receive(amqpContinuation(nil, false), &links, now); c != "" || !l.inbound.complete {
		t.Fatal(c)
	}
	s, links = amqpTransferFixture(t)
	l = links.links[0]
	l.credit = 1
	if c := s.receive(amqpTransfer(6, nil, true), &links, now); c != "" {
		t.Fatal(c)
	}
	for i := 1; i < amqpMessageFrames; i++ {
		if err := s.requestFlow(l, false, amqpCheckWrite(t)); err != nil {
			t.Fatal(err)
		}
		if c := s.receive(amqpContinuation(nil, true), &links, now); c != "" {
			t.Fatal(i, c)
		}
	}
	if c := s.receive(amqpContinuation(nil, true), &links, now); c != "amqp:resource-limit-exceeded" {
		t.Fatal(c)
	}
	s, links = amqpTransferFixture(t)
	links.links[0].credit = 1
	s.incomingWindow = 0
	if c := s.receive(amqpTransfer(7, nil, true), &links, now); c != "amqp:session:window-violation" {
		t.Fatal(c)
	}
}
func TestAMQPIncomingAbsoluteDeadlineAndDeliverySequence(t *testing.T) {
	s, links := amqpTransferFixture(t)
	l := links.links[0]
	l.credit = 1
	now := time.Now()
	if c := s.receive(amqpTransfer(9, nil, true), &links, now); c != "" {
		t.Fatal(c)
	}
	if err := s.requestFlow(l, false, amqpCheckWrite(t)); err != nil {
		t.Fatal(err)
	}
	if links.deliveryDeadline() != now.Add(amqpMessageDeadline) {
		t.Fatal("deadline moved")
	}
	if c := s.receive(amqpContinuation(nil, false), &links, now.Add(amqpMessageDeadline)); c != "amqp:resource-limit-exceeded" {
		t.Fatal(c)
	}
	for _, id := range []uint32{9, 8, 9 + 1<<31} {
		s, links = amqpTransferFixture(t)
		l = links.links[0]
		l.credit = 1
		l.haveIncomingID = true
		l.lastIncomingID = 9
		if c := s.receive(amqpTransfer(id, nil, false), &links, now); c != "amqp:invalid-field" {
			t.Fatal(id, c)
		}
	}
}

func TestAMQPIncomingRemoteWindowEstimateDoesNotInventCapacity(t *testing.T) {
	s, links := amqpTransferFixture(t)
	l := links.links[0]
	l.credit = 1
	now := time.Now()
	s.remoteOutgoing = 1
	if c := s.receive(amqpTransfer(10, nil, true), &links, now); c != "" {
		t.Fatal(c)
	}
	if err := s.requestFlow(l, false, amqpCheckWrite(t)); err != nil {
		t.Fatal(err)
	}
	if c := s.receive(amqpContinuation(nil, true), &links, now); c != "" || s.remoteOutgoing != -1 {
		t.Fatal(c, s)
	}
	if _, c := s.flow(amqpReceiverFlow(&s, nil, 0, false), &links); c != "" || s.remoteOutgoing != 8 {
		t.Fatal(c, s)
	}
	s.incomingWindow = 0
	if c := s.receive(amqpContinuation(nil, false), &links, now); c != "amqp:session:window-violation" {
		t.Fatal("peer estimate overrode our window", c)
	}
}
