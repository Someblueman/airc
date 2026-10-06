package adapter

import "github.com/Someblueman/airc/internal/gateway/amqpwire"

// Session sequence numbers count transfer frames, separately from link messages.
// The selected profile keeps at most eight transfer frames outstanding.
type amqpSession struct {
	nextIncoming, nextOutgoing     uint32
	remoteIncoming, remoteOutgoing uint32
}

func newAMQPSession(n amqpwire.Negotiation) amqpSession {
	return amqpSession{n.NextIncoming, 1, n.RemoteIncomingWindow, n.RemoteOutgoingWindow}
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
// Request credit stays zero until a durable signed dispatcher owns the delivery.
func (s *amqpSession) flow(p amqpwire.Performative, links *amqpLinks) (amqpwire.Performative, string) {
	if p.Kind != 0x13 || amqpField(p, 0).Code == 0x40 {
		return amqpwire.Performative{}, "amqp:invalid-field"
	}
	incoming, ok := amqpCredit(s.nextOutgoing, uint32(p.Fields[0].Number), uint32(p.Fields[1].Number))
	if !ok || p.Fields[3].Number >= 1<<31 {
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
		for _, l := range links.links {
			if l != nil && l.peerHandle == uint32(handle.Number) {
				link = l
				break
			}
		}
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
	nextCount := uint32(0)
	drain := link != nil && !link.incoming && amqpField(p, 8).Number != 0
	if link != nil {
		nextCount = link.deliveryCount
		if drain {
			nextCount += credit
		}
	}
	reply := amqpwire.Performative{}
	if amqpField(p, 9).Number != 0 || drain {
		reply = amqpwire.Performative{Kind: 0x13, Fields: []amqpwire.Value{{Code: 0x70, Number: p.Fields[2].Number}, {Code: 0x70, Number: 8}, {Code: 0x70, Number: uint64(s.nextOutgoing)}, {Code: 0x70, Number: 8}}}
		if link != nil {
			reply.Fields = append(reply.Fields, amqpwire.Value{Code: 0x70, Number: uint64(link.handle)}, amqpwire.Value{Code: 0x70, Number: uint64(nextCount)}, amqpwire.Value{Code: 0x70}, amqpwire.Value{Code: 0x70}, amqpwire.Value{Code: 0x56})
			if !link.incoming && !drain {
				reply.Fields[6].Number = uint64(credit)
			}
			if drain {
				reply.Fields[8].Number = 1
			}
		}
		if _, err := amqpwire.EncodePerformative(reply, 0, links.negotiation.PeerFrameLimit); err != nil {
			return amqpwire.Performative{}, "amqp:frame-size-too-small"
		}
	}
	s.nextIncoming, s.remoteIncoming, s.remoteOutgoing = uint32(p.Fields[2].Number), incoming, uint32(p.Fields[3].Number)
	if link != nil && !link.incoming {
		link.credit = credit
		if drain {
			link.deliveryCount, link.credit = nextCount, 0
		}
	}
	return reply, ""
}
