package adapter

import (
	"errors"
	"time"

	"github.com/Someblueman/airc/internal/gateway/amqpwire"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

func (c *amqpConnection) serveRequests(options config.AMQP, d *dispatch.Dispatcher, sealer *auth.ResponseSealer, realm string, requests chan struct{}) {
	session := newAMQPSession(c.negotiation)
	links := amqpLinks{principal: c.identity.ID(), routes: options.Routes, negotiation: c.negotiation}
	for {
		p, err := c.readBefore(links.deliveryDeadline())
		if err != nil {
			if errors.Is(err, amqpwire.ErrFrame) || errors.Is(err, amqpwire.ErrValue) || errors.Is(err, amqpwire.ErrNegotiation) {
				c.reject("amqp:connection:framing-error")
			}
			return
		}
		switch p.Kind {
		case 0:
			continue
		case 0x18:
			_ = c.write(amqpwire.Performative{Kind: 0x18})
			return
		case 0x17:
			if c.write(amqpwire.Performative{Kind: 0x17}) == nil {
				c.reject("")
			}
			return
		case 0x10, 0x11:
			c.reject("amqp:illegal-state")
			return
		case 0x12:
			reply, condition := links.attach(p)
			if condition != "" {
				c.reject(condition)
				return
			}
			if c.write(reply) != nil {
				return
			}
		case 0x13:
			reply, condition := session.flow(p, &links)
			if condition != "" {
				c.endSession(condition)
				return
			}
			if reply.Kind != 0 && c.write(reply) != nil {
				return
			}
		case 0x14:
			l := links.peerLink(uint32(p.Fields[0].Number))
			condition := session.receive(p, &links, time.Now())
			if condition != "" {
				if condition == "amqp:session:unattached-handle" || condition == "amqp:session:window-violation" {
					c.endSession(condition)
				} else {
					c.detachAMQP(l, condition)
				}
				return
			}
			if l.inbound != nil && l.inbound.complete {
				if !c.dispatchAMQP(&session, &links, l, d, sealer, realm, requests) {
					return
				}
			} else if session.incomingWindow == 0 {
				if session.requestFlow(l, false, c.write) != nil {
					return
				}
			}
		case 0x15:
			if condition := links.disposition(p); condition != "" {
				c.endSession(condition)
				return
			}
		case 0x16:
			l := links.peerLink(uint32(p.Fields[0].Number))
			if l == nil {
				c.endSession("amqp:session:unattached-handle")
				return
			}
			// Link recovery is outside this volatile fixed-pair profile.
			_ = c.write(amqpwire.Performative{Kind: 0x16, Fields: []amqpwire.Value{{Code: 0x70, Number: uint64(l.handle)}, {Code: 0x56, Number: 1}}})
			c.reject("")
			return
		default:
			c.reject("amqp:not-implemented")
			return
		}
		in, out := links.pair()
		if out != nil {
			if session.flushResponse(out, c.negotiation.PeerFrameLimit, time.Now(), c.writeBefore) != nil {
				return
			}
			if pending := out.outbound; pending != nil && pending.complete && pending.settled {
				clear(pending.encoded)
				clear(pending.tag)
				out.outbound = nil
			}
		}
		// One request and one result are retained per connection. Response credit
		// may arrive later; its fixed deadline bounds that wait and settlement.
		if in != nil && out != nil && in.inbound == nil && out.outbound == nil && in.credit == 0 {
			if session.requestFlow(in, true, c.write) != nil {
				return
			}
		}
	}
}

func (s *amqpLinks) pair() (in, out *amqpLink) {
	for _, l := range s.links {
		if l != nil {
			if l.incoming {
				in = l
			} else {
				out = l
			}
		}
	}
	return
}

func (c *amqpConnection) detachAMQP(l *amqpLink, condition string) {
	if l != nil {
		_ = c.write(amqpwire.Performative{Kind: 0x16, Fields: []amqpwire.Value{{Code: 0x70, Number: uint64(l.handle)}, {Code: 0x56, Number: 1}, amqpError(condition)}})
	}
	c.reject("")
}

func (c *amqpConnection) dispatchAMQP(session *amqpSession, links *amqpLinks, in *amqpLink, d *dispatch.Dispatcher, sealer *auth.ResponseSealer, realm string, requests chan struct{}) bool {
	_, out := links.pair()
	if out == nil || out.outbound != nil || links.route == nil {
		c.detachAMQP(in, "amqp:illegal-state")
		return false
	}
	admission := admitAMQP(c.ctx, d, c.identity, *links.route, in.inbound, requests)
	if admission.condition != "" {
		c.detachAMQP(in, admission.condition)
		return false
	}
	now := time.Now()
	scope := amqpResponseScope(realm, c.identity.ID(), c.identity.Binding(), *links.route)
	encoded, err := amqpResultMessage(sealer, scope, *links.route, admission.requestID, admission.result, admission.failure, now)
	if errors.Is(err, auth.ErrResponseCapacity) || err == nil && uint64(len(encoded)) > out.messageLimit {
		encoded, err = amqpResultMessage(sealer, scope, *links.route, admission.requestID, nil, &core.Failure{Code: core.CapacityExceeded}, now)
	}
	if err != nil {
		c.detachAMQP(out, "amqp:internal-error")
		return false
	}
	if condition := session.queueResponse(out, encoded, in.inbound.tag, now); condition != "" {
		c.detachAMQP(out, condition)
		return false
	}
	state := amqpOutcome(0x24, "")
	if admission.failure != nil {
		if admission.failure.Transient {
			state = amqpOutcome(0x26, "")
		} else {
			state = amqpOutcome(0x25, "amqp:precondition-failed")
		}
	}
	// Send acceptance is emitted only after the dispatcher returns durable intake.
	if !in.inbound.settled && c.write(amqpRequestDisposition(in.inbound.id, state)) != nil {
		return false
	}
	clear(in.inbound.encoded)
	clear(in.inbound.tag)
	in.inbound = nil
	return session.requestFlow(in, false, c.write) == nil
}
