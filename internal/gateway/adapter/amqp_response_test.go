package adapter

import (
	"bytes"
	"errors"
	"github.com/Someblueman/airc/internal/gateway/amqpwire"
	"testing"
	"time"
)

func amqpReceiverFlow(s *amqpSession, l *amqpLink, credit uint32, drain bool) amqpwire.Performative {
	p := amqpwire.Performative{Kind: 0x13, Fields: []amqpwire.Value{{Code: 0x70, Number: uint64(s.nextOutgoing)}, {Code: 0x70, Number: 8}, {Code: 0x70, Number: uint64(s.nextIncoming)}, {Code: 0x70, Number: 8}}}
	if l != nil {
		p.Fields = append(p.Fields, amqpwire.Value{Code: 0x70, Number: uint64(l.peerHandle)}, amqpwire.Value{Code: 0x70, Number: uint64(l.deliveryCount)}, amqpwire.Value{Code: 0x70, Number: uint64(credit)}, amqpwire.Value{Code: 0x40}, amqpwire.Value{Code: 0x56}, amqpwire.Value{Code: 0x56, Number: 1})
		if drain {
			p.Fields[8].Number = 1
		}
	}
	return p
}
func TestAMQPResponseMultipartHonorsBothWindowsAndMessageCredit(t *testing.T) {
	s, links := amqpTransferFixture(t)
	l := links.links[1]
	now := time.Now()
	body := bytes.Repeat([]byte("x"), amqpwire.MaxMessageBody)
	encoded, err := amqpwire.EncodeDataMessage(body)
	if err != nil {
		t.Fatal(err)
	}
	tag := []byte("response")
	if c := s.queueResponse(l, encoded, tag, now); c != "" {
		t.Fatal(c)
	}
	tag[0] = 'X'
	encoded[0] = 'X'
	if string(l.outbound.tag) != "response" {
		t.Fatal("borrowed tag")
	}
	var result []byte
	frames := 0
	write := func(p amqpwire.Performative, deadline time.Time) error {
		if deadline != now.Add(amqpMessageDeadline) {
			t.Fatal("deadline changed")
		}
		frame, err := amqpwire.EncodePerformative(p, 0, 512)
		if err != nil {
			return err
		}
		if len(frame.Body)+8 > 512 {
			t.Fatal("exceeded peer frame limit")
		}
		p, err = amqpwire.DecodePerformative(frame)
		if err != nil {
			return err
		}
		if frames == 0 {
			if p.Fields[1].Number != 0 || string(p.Fields[2].Data) != "response" || p.Fields[3].Code != 0x70 {
				t.Fatal(p)
			}
		} else if p.Fields[1].Code != 0x40 || p.Fields[2].Code != 0x40 {
			t.Fatal("repeated first fields")
		}
		result = append(result, p.Payload...)
		frames++
		return nil
	}
	if err := s.flushResponse(l, 512, now, write); err != nil || frames != 0 {
		t.Fatal(err, frames)
	}
	if _, c := s.flow(amqpReceiverFlow(&s, l, 1, false), &links); c != "" {
		t.Fatal(c)
	}
	s.remoteIncoming = 100
	if err := s.flushResponse(l, 512, now, write); err != nil || frames != 8 || s.outgoingWindow != 0 || s.remoteIncoming != 92 || l.credit != 0 || l.deliveryCount != 1 {
		t.Fatal(err, s, l, frames)
	}
	for !l.outbound.complete {
		if _, c := s.flow(amqpReceiverFlow(&s, nil, 0, false), &links); c != "" {
			t.Fatal(c)
		}
		before := frames
		if err := s.flushResponse(l, 512, now, write); err != nil || frames-before > 8 || frames == before {
			t.Fatal(err, frames)
		}
	}
	message, err := amqpwire.DecodeMessage(result)
	if err != nil || !bytes.Equal(message.Body, body) || s.nextOutgoing != uint32(frames+1) || l.deliveryCount != 1 || s.nextDeliveryID != 1 {
		t.Fatal(err, s, l, frames)
	}
	if c := s.queueResponse(l, []byte("second"), nil, now); c != "amqp:resource-limit-exceeded" {
		t.Fatal("overwrote unsettled result", c)
	}
}
func TestAMQPResponseWriteFailureDoesNotAdvance(t *testing.T) {
	s, links := amqpTransferFixture(t)
	l := links.links[1]
	now := time.Now()
	l.credit = 1
	if c := s.queueResponse(l, []byte("result"), nil, now); c != "" {
		t.Fatal(c)
	}
	before := s
	old := *l
	failure := errors.New("short write")
	if err := s.flushResponse(l, 512, now, func(amqpwire.Performative, time.Time) error { return failure }); !errors.Is(err, failure) || s != before || *l != old || l.outbound.offset != 0 {
		t.Fatal(err, s, l)
	}
}
func TestAMQPResponseDrainSendsAvailableMessageBeforeAdvancing(t *testing.T) {
	s, links := amqpTransferFixture(t)
	l := links.links[1]
	now := time.Now()
	if c := s.queueResponse(l, []byte("result"), nil, now); c != "" {
		t.Fatal(c)
	}
	r, c := s.flow(amqpReceiverFlow(&s, l, 9, true), &links)
	if c != "" || r.Fields[6].Number != 9 || r.Fields[7].Number != 1 || !l.drainPending || l.deliveryCount != 0 {
		t.Fatal(r, c, l)
	}
	var written []amqpwire.Performative
	if err := s.flushResponse(l, 512, now, func(p amqpwire.Performative, _ time.Time) error {
		written = append(written, p)
		return amqpCheckWrite(t)(p)
	}); err != nil {
		t.Fatal(err)
	}
	if len(written) != 2 || written[0].Kind != 0x14 || written[1].Kind != 0x13 || written[1].Fields[5].Number != 9 || written[1].Fields[6].Number != 0 || l.credit != 0 || l.deliveryCount != 9 || l.drainPending {
		t.Fatal(written, l)
	}
}
