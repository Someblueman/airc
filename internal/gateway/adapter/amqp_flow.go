package adapter

import "github.com/Someblueman/airc/internal/gateway/amqpwire"

// Session sequence numbers count transfer frames, separately from link messages.
// The selected profile keeps at most eight transfer frames outstanding.
type amqpSession struct {
	nextIncoming, nextOutgoing     uint32
	remoteIncoming                 uint32
	remoteOutgoing                 int64
	incomingWindow, outgoingWindow uint32
	ackOutgoing, nextDeliveryID    uint32
}

func newAMQPSession(n amqpwire.Negotiation) amqpSession {
	return amqpSession{nextIncoming: n.NextIncoming, nextOutgoing: 1, remoteIncoming: n.RemoteIncomingWindow, remoteOutgoing: int64(n.RemoteOutgoingWindow), incomingWindow: amqpWindow, outgoingWindow: amqpWindow, ackOutgoing: 1}
}

// Credit is a serial-number distance, never unsigned subtraction of a negative
// allowance. Half-space windows cannot be ordered unambiguously in this profile.
func amqpCredit(count, acknowledged, window uint32) (uint32, bool) {
	if window >= 1<<31 {
		return 0, false
	}
	outstanding := count - acknowledged
	if outstanding >= 1<<31 {
		return 0, false
	}
	if outstanding >= window {
		return 0, true
	}
	return window - outstanding, true
}

// flow accepts codec-validated fields. Validation precedes all state changes.
// Request credit stays zero until a durable dispatcher owns the delivery.
func (s *amqpSession) flow(p amqpwire.Performative, links *amqpLinks) (amqpwire.Performative, string) {
	if p.Kind != 0x13 || amqpField(p, 0).Code == 0x40 {
		return amqpwire.Performative{}, "amqp:invalid-field"
	}
	incoming, ok := amqpCredit(s.nextOutgoing, uint32(p.Fields[0].Number), uint32(p.Fields[1].Number))
	if !ok || p.Fields[3].Number >= 1<<31 {
		return amqpwire.Performative{}, "amqp:invalid-field"
	}
	ack := uint32(p.Fields[0].Number)
	if ack-s.ackOutgoing > s.nextOutgoing-s.ackOutgoing {
		return amqpwire.Performative{}, "amqp:invalid-field"
	}
	var link *amqpLink
	credit := uint32(0)
	handle := amqpField(p, 4)
	if handle.Code == 0x40 {
		for _, i := range []int{5, 6, 7, 8, 10} {
			if amqpField(p, i).Code != 0x40 {
				return amqpwire.Performative{}, "amqp:invalid-field"
			}
		}
	} else {
		link = links.peerLink(uint32(handle.Number))
		if link == nil {
			return amqpwire.Performative{}, "amqp:session:unattached-handle"
		}
		count := amqpField(p, 5)
		if link.incoming {
			if count.Code == 0x40 || uint32(count.Number) != link.deliveryCount || amqpField(p, 8).Number != 0 {
				return amqpwire.Performative{}, "amqp:invalid-field"
			}
		} else {
			acknowledged := uint32(0) // Receiver Flow may race the initial sender Attach.
			if count.Code != 0x40 {
				acknowledged = uint32(count.Number)
			}
			if amqpField(p, 6).Code == 0x40 {
				return amqpwire.Performative{}, "amqp:invalid-field"
			}
			credit, ok = amqpCredit(link.deliveryCount, acknowledged, uint32(amqpField(p, 6).Number))
			if !ok {
				return amqpwire.Performative{}, "amqp:invalid-field"
			}
		}
	}
	next := *s
	next.nextIncoming, next.remoteIncoming, next.remoteOutgoing = uint32(p.Fields[2].Number), incoming, int64(p.Fields[3].Number)
	next.ackOutgoing, next.outgoingWindow = ack, amqpWindow-(s.nextOutgoing-ack)
	count := uint32(0)
	drain := link != nil && !link.incoming && amqpField(p, 8).Number != 0
	pending := drain && link.outbound != nil && !link.outbound.complete
	if link != nil {
		count = link.deliveryCount
		if drain && !pending {
			count += credit
			credit = 0
		}
	}
	reply := amqpwire.Performative{}
	if amqpField(p, 9).Number != 0 || drain && !pending {
		reply = next.stateFlow(link, count, credit, drain, next.incomingWindow)
		if _, err := amqpwire.EncodePerformative(reply, 0, links.negotiation.PeerFrameLimit); err != nil {
			return amqpwire.Performative{}, "amqp:frame-size-too-small"
		}
	}
	*s = next
	if link != nil && !link.incoming {
		link.deliveryCount, link.credit, link.drain, link.drainPending = count, credit, drain, pending
	}

	return reply, ""
}

// stateFlow uses local output handles and never asks for an echo of its reply.
func (s *amqpSession) stateFlow(link *amqpLink, count, credit uint32, drain bool, window uint32) amqpwire.Performative {
	p := amqpwire.Performative{Kind: 0x13, Fields: []amqpwire.Value{{Code: 0x70, Number: uint64(s.nextIncoming)}, {Code: 0x70, Number: uint64(window)}, {Code: 0x70, Number: uint64(s.nextOutgoing)}, {Code: 0x70, Number: uint64(s.outgoingWindow)}}}
	if link != nil {
		p.Fields = append(p.Fields, amqpwire.Value{Code: 0x70, Number: uint64(link.handle)}, amqpwire.Value{Code: 0x70, Number: uint64(count)}, amqpwire.Value{Code: 0x70, Number: uint64(credit)})
		if !link.incoming {
			available := uint64(0)
			if link.outbound != nil && !link.outbound.complete {
				available = 1
			}
			p.Fields = append(p.Fields, amqpwire.Value{Code: 0x70, Number: available}, amqpwire.Value{Code: 0x56})
			if drain {
				p.Fields[8].Number = 1
			}
		}
	}
	return p
}
