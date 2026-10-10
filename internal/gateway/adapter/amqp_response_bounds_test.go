package adapter

import (
	"bytes"
	"github.com/Someblueman/airc/internal/gateway/amqpwire"
	"testing"
	"time"
)

func TestAMQPResponseQueueLimitsAndExpiry(t *testing.T) {
	s, links := amqpTransferFixture(t)
	l := links.links[1]
	now := time.Now()
	for _, tc := range []struct {
		data, tag []byte
		want      string
	}{
		{nil, nil, "amqp:invalid-field"}, {[]byte("ok"), make([]byte, 33), "amqp:invalid-field"}, {make([]byte, 65537), nil, "amqp:link:message-size-exceeded"},
	} {
		if c := s.queueResponse(l, tc.data, tc.tag, now); c != tc.want || l.outbound != nil {
			t.Fatal(c)
		}
	}
	l.messageLimit = 2
	if c := s.queueResponse(l, []byte("big"), nil, now); c != "amqp:link:message-size-exceeded" {
		t.Fatal(c)
	}
	if c := s.queueResponse(l, []byte("ok"), nil, now); c != "" {
		t.Fatal(c)
	}
	before := s
	if err := s.flushResponse(l, 512, now.Add(amqpMessageDeadline), func(amqpwire.Performative, time.Time) error { t.Fatal("wrote expired response"); return nil }); err == nil || s != before {
		t.Fatal(err)
	}
	if links.deliveryDeadline() != now.Add(amqpMessageDeadline) {
		t.Fatal("lost deadline")
	}
}
func TestAMQPResponseRemoteWindowAndWraparound(t *testing.T) {
	s, links := amqpTransferFixture(t)
	l := links.links[1]
	now := time.Now()
	s.nextOutgoing, s.ackOutgoing, s.nextDeliveryID = 0xffffffff, 0xffffffff, 0xffffffff
	s.remoteIncoming = 1
	l.credit = 1
	l.deliveryCount = 0xffffffff
	if c := s.queueResponse(l, bytes.Repeat([]byte{1}, 1024), nil, now); c != "" {
		t.Fatal(c)
	}
	frames := 0
	write := func(p amqpwire.Performative, _ time.Time) error { frames++; return amqpCheckWrite(t)(p) }
	if err := s.flushResponse(l, 512, now, write); err != nil || frames != 1 || s.nextOutgoing != 0 || s.nextDeliveryID != 0 || l.deliveryCount != 0 || l.credit != 0 || s.outgoingWindow != 7 {
		t.Fatal(err, s, l, frames)
	}
	if err := s.flushResponse(l, 512, now, write); err != nil || frames != 1 {
		t.Fatal(err, frames)
	}
	if _, c := s.flow(amqpReceiverFlow(&s, nil, 0, false), &links); c != "" || s.outgoingWindow != 8 {
		t.Fatal(c, s)
	}
	if err := s.flushResponse(l, 512, now, write); err != nil || !l.outbound.complete || l.deliveryCount != 0 || s.nextDeliveryID != 0 {
		t.Fatal(err, l)
	}
	before := s
	p := amqpReceiverFlow(&s, nil, 0, false)
	p.Fields[0].Number = 0xffffffff
	if _, c := s.flow(p, &links); c != "amqp:invalid-field" || s != before {
		t.Fatal("accepted backward acknowledgement", c)
	}
}
