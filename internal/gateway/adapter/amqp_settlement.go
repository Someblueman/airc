package adapter

import "github.com/Someblueman/airc/internal/gateway/amqpwire"

// Dispositions use session delivery IDs, never request replay identity. Ranges
// are checked without iterating peer-controlled spans, including serial wrap.
func (s *amqpLinks) disposition(p amqpwire.Performative) string {
	first := uint32(amqpField(p, 1).Number)
	last := first
	if v := amqpField(p, 2); v.Code != 0x40 {
		last = uint32(v.Number)
	}
	if last-first >= 1<<31 {
		return "amqp:invalid-field"
	}
	receiver := amqpField(p, 0).Number != 0
	settled := amqpField(p, 3).Number != 0
	state := amqpField(p, 4)
	for _, l := range s.links {
		if l == nil || l.incoming == receiver {
			continue
		}
		if receiver {
			out := l.outbound
			if out == nil || out.offset == 0 || out.id-first > last-first {
				continue
			}
			// Receiver-first settlement may arrive while a response is fragmented.
			// Retain its bytes and deadline until the remaining transfer is complete.
			if settled {
				out.settled = true
			}
		} else {
			in := l.inbound
			if in == nil || in.id-first > last-first {
				continue
			}
			// The selected ingress profile accepts settlement notifications, but
			// sender-proposed delivery states cannot replace request authority.
			if state.Code != 0x40 {
				return "amqp:not-implemented"
			}
			if settled {
				in.settled = true
			}
		}
	}
	return ""
}

func amqpOutcome(code uint64, condition string) amqpwire.Value {
	fields := []amqpwire.Value{}
	if condition != "" {
		fields = append(fields, amqpError(condition))
	}
	return amqpwire.Value{Descriptor: &amqpwire.Value{Code: 0x80, Number: code}, Children: []amqpwire.Value{{Code: 0xc0, Children: fields}}}
}

func amqpRequestDisposition(id uint32, state amqpwire.Value) amqpwire.Performative {
	return amqpwire.Performative{Kind: 0x15, Fields: []amqpwire.Value{{Code: 0x56, Number: 1}, {Code: 0x70, Number: uint64(id)}, {Code: 0x40}, {Code: 0x56, Number: 1}, state}}
}
