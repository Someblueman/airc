package adapter

import (
	"bytes"
	"time"

	"github.com/Someblueman/airc/internal/gateway/amqpwire"
)

const (
	amqpWindow          = 8
	amqpMessageFrames   = 256
	amqpMessageDeadline = 30 * time.Second
)

type amqpIncoming struct {
	id           uint32
	tag, encoded []byte
	frames       int
	complete     bool
	deadline     time.Time
}

// receive accepts codec-validated transfers. One request remains owned by this
// link until its caller has committed intake and written the final disposition.
// Assembly itself neither authenticates a message nor acknowledges receipt.
func (s *amqpSession) receive(p amqpwire.Performative, links *amqpLinks, now time.Time) string {
	if p.Kind != 0x14 || len(p.Fields) == 0 {
		return "amqp:invalid-field"
	}
	l := links.peerLink(uint32(p.Fields[0].Number))
	if l == nil {
		return "amqp:session:unattached-handle"
	}
	if !l.incoming {
		return "amqp:invalid-field"
	}
	if s.incomingWindow == 0 {
		return "amqp:session:window-violation"
	}
	if amqpField(p, 6).Number != 0 || amqpField(p, 7).Code != 0x40 || amqpField(p, 8).Number != 0 {
		return "amqp:not-implemented"
	}
	aborted := amqpField(p, 9).Number != 0
	if amqpField(p, 4).Number != 0 && !aborted {
		return "amqp:invalid-field"
	}
	id, tag, format := amqpField(p, 1), amqpField(p, 2), amqpField(p, 3)
	current := l.inbound
	if current == nil {
		if l.credit == 0 {
			return "amqp:link:transfer-limit-exceeded"
		}
		if id.Code == 0x40 || tag.Code == 0x40 || format.Code == 0x40 {
			return "amqp:invalid-field"
		}
		if l.haveIncomingID {
			distance := uint32(id.Number) - l.lastIncomingID
			if distance == 0 || distance >= 1<<31 {
				return "amqp:invalid-field"
			}
		}
		current = &amqpIncoming{id: uint32(id.Number), deadline: now.Add(amqpMessageDeadline)}
	} else {
		if current.complete {
			return "amqp:link:transfer-limit-exceeded"
		}
		if !now.Before(current.deadline) {
			return "amqp:resource-limit-exceeded"
		}
		if id.Code != 0x40 && uint32(id.Number) != current.id || tag.Code != 0x40 && !bytes.Equal(tag.Data, current.tag) || format.Code != 0x40 && format.Number != 0 {
			return "amqp:invalid-field"
		}
	}
	if current.frames >= amqpMessageFrames {
		return "amqp:resource-limit-exceeded"
	}
	if !aborted && uint64(len(p.Payload)) > l.messageLimit-uint64(len(current.encoded)) {
		return "amqp:link:message-size-exceeded"
	}
	if l.inbound == nil {
		current.tag = bytes.Clone(tag.Data)
		if !aborted {
			current.encoded = make([]byte, 0, int(l.messageLimit))
		}
		l.inbound = current
		l.credit--
		l.deliveryCount++
		l.lastIncomingID, l.haveIncomingID = current.id, true
	}
	s.nextIncoming++
	s.incomingWindow--
	// This is the peer's last-advertised estimate, not admission capacity.
	// Its sender may keep outgoing-window constant without sending a Flow.
	s.remoteOutgoing--
	current.frames++
	if aborted {
		clear(current.encoded)
		clear(current.tag)
		l.inbound = nil // Abortion settles the delivery and ignores this payload.
		return ""
	}
	current.encoded = append(current.encoded, p.Payload...)
	current.complete = amqpField(p, 5).Number == 0
	return ""
}

// requestFlow refreshes storage-backed session capacity and, when explicitly
// requested by the durable dispatcher, grants one message. Failed writes do not
// change counters. No listener grants request credit before dispatch is wired.
func (s *amqpSession) requestFlow(l *amqpLink, grant bool, write func(amqpwire.Performative) error) error {
	if l == nil || !l.incoming || grant && l.inbound != nil {
		return amqpwire.ErrNegotiation
	}
	credit := l.credit
	if grant {
		credit = 1
	}
	p := s.stateFlow(l, l.deliveryCount, credit, false, amqpWindow)
	if err := write(p); err != nil {
		return err
	}
	s.incomingWindow, l.credit = amqpWindow, credit
	return nil
}

func (s *amqpLinks) peerLink(handle uint32) *amqpLink {
	for _, l := range s.links {
		if l != nil && l.peerHandle == handle {
			return l
		}
	}
	return nil
}

// A partial assembly or an unsettled response cannot be kept alive indefinitely
// by heartbeats or zero-byte continuations. The owner applies this to every read.
func (s *amqpLinks) deliveryDeadline() time.Time {
	var deadline time.Time
	for _, l := range s.links {
		if l == nil {
			continue
		}
		if l.inbound != nil && !l.inbound.complete {
			deadline = amqpEarlier(deadline, l.inbound.deadline)
		}
		if l.outbound != nil {
			deadline = amqpEarlier(deadline, l.outbound.deadline)
		}
	}
	return deadline
}

func amqpEarlier(a, b time.Time) time.Time {
	if a.IsZero() || !b.IsZero() && b.Before(a) {
		return b
	}
	return a
}
