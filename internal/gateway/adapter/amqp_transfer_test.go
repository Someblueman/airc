package adapter

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/amqpwire"
)

func amqpTransferFixture(t *testing.T) (amqpSession, amqpLinks) {
	t.Helper()
	links := amqpLinkFixture()
	for _, p := range []amqpwire.Performative{amqpAttach(true, 1, "request"), amqpAttach(false, 0, "response")} {
		if _, c := links.attach(p); c != "" {
			t.Fatal(c)
		}
	}
	return newAMQPSession(amqpwire.Negotiation{NextIncoming: 7, RemoteIncomingWindow: 8, RemoteOutgoingWindow: 8}), links
}
func amqpTransfer(id uint32, payload []byte, more bool) amqpwire.Performative {
	p := amqpwire.Performative{Kind: 0x14, Fields: []amqpwire.Value{{Code: 0x70, Number: 1}, {Code: 0x70, Number: uint64(id)}, {Code: 0xa0, Data: []byte("tag")}, {Code: 0x70}, {Code: 0x56}, {Code: 0x56}}, Payload: payload}
	if more {
		p.Fields[5].Number = 1
	}
	return p
}
func amqpContinuation(payload []byte, more bool) amqpwire.Performative {
	p := amqpTransfer(0, payload, more)
	for i := 1; i <= 4; i++ {
		p.Fields[i] = amqpwire.Value{Code: 0x40}
	}
	return p
}
func amqpCheckWrite(t *testing.T) func(amqpwire.Performative) error {
	t.Helper()
	return func(p amqpwire.Performative) error {
		f, err := amqpwire.EncodePerformative(p, 0, 512)
		if err != nil {
			return err
		}
		_, err = amqpwire.DecodePerformative(f)
		return err
	}
}
func TestAMQPIncomingMultipartOwnsBytesAndCredit(t *testing.T) {
	s, links := amqpTransferFixture(t)
	l := links.links[0]
	now := time.Now()
	if err := s.requestFlow(l, true, amqpCheckWrite(t)); err != nil {
		t.Fatal(err)
	}
	first := amqpTransfer(42, []byte("one"), true)
	if c := s.receive(first, &links, now); c != "" {
		t.Fatal(c)
	}
	first.Payload[0] = 'X'
	first.Fields[2].Data[0] = 'X'
	if string(l.inbound.encoded) != "one" || string(l.inbound.tag) != "tag" || l.credit != 0 || l.deliveryCount != 8 || s.nextIncoming != 8 {
		t.Fatal(l, s)
	}
	if err := s.requestFlow(l, true, amqpCheckWrite(t)); err == nil {
		t.Fatal("granted another unsettled delivery")
	}
	if c := s.receive(amqpContinuation([]byte("two"), false), &links, now.Add(time.Second)); c != "" {
		t.Fatal(c)
	}
	if !l.inbound.complete || string(l.inbound.encoded) != "onetwo" || l.inbound.frames != 2 || l.deliveryCount != 8 || s.nextIncoming != 9 || s.incomingWindow != 6 || s.remoteOutgoing != 6 || cap(l.inbound.encoded) != 65536 {
		t.Fatal(l, s)
	}
	if c := s.receive(amqpTransfer(43, nil, false), &links, now); c != "amqp:link:transfer-limit-exceeded" {
		t.Fatal(c)
	}
}
func TestAMQPIncomingAbortIgnoresPayloadAndWraps(t *testing.T) {
	s, links := amqpTransferFixture(t)
	l := links.links[0]
	now := time.Now()
	s.nextIncoming = 0xffffffff
	l.deliveryCount = 0xffffffff
	l.messageLimit = 2
	if err := s.requestFlow(l, true, amqpCheckWrite(t)); err != nil {
		t.Fatal(err)
	}
	if c := s.receive(amqpTransfer(0xffffffff, []byte("hi"), true), &links, now); c != "" {
		t.Fatal(c)
	}
	old := l.inbound
	p := amqpContinuation(bytes.Repeat([]byte("x"), 1024), true)
	p.Fields = append(p.Fields, amqpwire.Value{Code: 0x40}, amqpwire.Value{Code: 0x40}, amqpwire.Value{Code: 0x56}, amqpwire.Value{Code: 0x56, Number: 1})
	p.Fields[4] = amqpwire.Value{Code: 0x56, Number: 1}
	if c := s.receive(p, &links, now); c != "" {
		t.Fatal(c)
	}
	if l.inbound != nil || l.credit != 0 || l.deliveryCount != 0 || s.nextIncoming != 1 || !bytes.Equal(old.encoded, []byte{0, 0}) || !bytes.Equal(old.tag, []byte{0, 0, 0}) {
		t.Fatal(l, s, old)
	}
	if err := s.requestFlow(l, true, amqpCheckWrite(t)); err != nil {
		t.Fatal(err)
	}
	if c := s.receive(amqpTransfer(0, []byte("ok"), false), &links, now); c != "" || !l.inbound.complete {
		t.Fatal(c)
	}
}
func TestAMQPRequestFlowWriteFailurePreservesCapacity(t *testing.T) {
	s, links := amqpTransferFixture(t)
	l := links.links[0]
	s.incomingWindow = 0
	before := s
	failure := errors.New("write failed")
	if err := s.requestFlow(l, true, func(amqpwire.Performative) error { return failure }); !errors.Is(err, failure) || s != before || l.credit != 0 {
		t.Fatal(err, s, l)
	}
}
