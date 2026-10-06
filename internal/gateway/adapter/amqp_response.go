package adapter

import (
	"bytes"
	"time"

	"github.com/Someblueman/airc/internal/gateway/amqpwire"
)

type amqpOutgoing struct {
	id           uint32
	tag, encoded []byte
	offset       int
	complete     bool
	deadline     time.Time
}

// queueResponse owns one encoded result until receiver settlement. Delivery IDs
// are session scoped; tags are volatile wire identifiers, never replay identity.
func (s *amqpSession) queueResponse(l *amqpLink, encoded, tag []byte, now time.Time) string {
	if l == nil || l.incoming {
		return "amqp:invalid-field"
	}
	if l.outbound != nil {
		return "amqp:resource-limit-exceeded"
	}
	if len(tag) > 32 || len(encoded) == 0 {
		return "amqp:invalid-field"
	}
	if uint64(len(encoded)) > l.messageLimit || len(encoded) > amqpwire.MaxFrameBytes {
		return "amqp:link:message-size-exceeded"
	}
	l.outbound = &amqpOutgoing{id: s.nextDeliveryID, tag: bytes.Clone(tag), encoded: bytes.Clone(encoded), deadline: now.Add(amqpMessageDeadline)}
	return ""
}

// flushResponse writes at most the advertised local/remote frame windows. A
// continuation needs session capacity, but does not consume another link credit.
// Counters advance only after a complete write; any write failure closes the
// connection at the caller, so a partial frame is never retried on that stream.
func (s *amqpSession) flushResponse(l *amqpLink, frameLimit uint32, now time.Time, write func(amqpwire.Performative, time.Time) error) error {
	if l == nil || l.incoming {
		return amqpwire.ErrNegotiation
	}
	out := l.outbound
	if out == nil {
		return nil
	}
	if !now.Before(out.deadline) {
		return amqpwire.ErrNegotiation
	}
	for !out.complete && s.outgoingWindow > 0 && s.remoteIncoming > 0 {
		first := out.offset == 0
		if first && l.credit == 0 {
			break
		}
		p := amqpwire.Performative{Kind: 0x14, Fields: []amqpwire.Value{{Code: 0x70, Number: uint64(l.handle)}, {Code: 0x40}, {Code: 0x40}, {Code: 0x40}, {Code: 0x40}, {Code: 0x56}}}
		if first {
			p.Fields[1], p.Fields[2], p.Fields[3], p.Fields[4] = amqpwire.Value{Code: 0x70, Number: uint64(out.id)}, amqpwire.Value{Code: 0xa0, Data: out.tag}, amqpwire.Value{Code: 0x70}, amqpwire.Value{Code: 0x56}
		}
		frame, err := amqpwire.EncodePerformative(p, 0, frameLimit)
		if err != nil {
			return err
		}
		capacity := int(frameLimit) - 8 - len(frame.Body)
		if capacity <= 0 {
			return amqpwire.ErrFrame
		}
		n := min(capacity, len(out.encoded)-out.offset)
		p.Payload = out.encoded[out.offset : out.offset+n]
		if out.offset+n < len(out.encoded) {
			p.Fields[5].Number = 1
		}
		if err := write(p, out.deadline); err != nil {
			return err
		}
		if first {
			l.credit--
			l.deliveryCount++
			s.nextDeliveryID++
		}
		out.offset += n
		out.complete = out.offset == len(out.encoded)
		s.nextOutgoing++
		s.outgoingWindow--
		s.remoteIncoming--
	}
	if out.complete && l.drainPending {
		count := l.deliveryCount + l.credit
		if err := write(s.stateFlow(l, count, 0, true, s.incomingWindow), out.deadline); err != nil {
			return err
		}
		l.deliveryCount, l.credit, l.drainPending = count, 0, false
	}
	return nil
}
