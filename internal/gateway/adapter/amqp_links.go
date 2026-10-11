package adapter

import (
	"slices"

	"github.com/Someblueman/airc/internal/gateway/amqpwire"
	"github.com/Someblueman/airc/internal/gateway/config"
)

type amqpLink struct {
	peerHandle, handle, deliveryCount uint32
	incoming                          bool
	messageLimit                      uint64
	credit                            uint32
	drain, drainPending               bool
	inbound                           *amqpIncoming
	outbound                          *amqpOutgoing
	lastIncomingID                    uint32
	haveIncomingID                    bool
}

// One volatile request/response pair binds the entire connection to one
// route. Both input and output handles are bounded; no delivery credit is
// granted here. The response link is optional: a send-only client gets only
// dispositions.
type amqpLinks struct {
	routes      []config.AMQPRoute
	route       *config.AMQPRoute
	negotiation amqpwire.Negotiation
	links       [2]*amqpLink
}

// attach receives a codec-validated performative. A refusal leaves state intact.
func (s *amqpLinks) attach(p amqpwire.Performative) (amqpwire.Performative, string) {
	if p.Kind != 0x12 || len(p.Fields) < 3 {
		return amqpwire.Performative{}, "amqp:invalid-field"
	}
	name, handle, role := amqpField(p, 0), amqpField(p, 1), amqpField(p, 2)
	if len(name.Data) == 0 || len(name.Data) > 128 {
		return amqpwire.Performative{}, "amqp:invalid-field"
	}
	if handle.Number > 1 {
		return amqpwire.Performative{}, "amqp:connection:framing-error"
	}
	incoming := role.Number == 0
	for _, l := range s.links {
		if l == nil {
			continue
		}
		if uint64(l.peerHandle) == handle.Number {
			return amqpwire.Performative{}, "amqp:session:handle-in-use"
		}
		if l.incoming == incoming {
			return amqpwire.Performative{}, "amqp:resource-limit-exceeded"
		}
	}
	snd, rcv := amqpField(p, 3), amqpField(p, 4)
	sndMode := uint64(2)
	if snd.Code != 0x40 {
		sndMode = snd.Number
	}
	// A sender may choose any settlement mode: settled transfers are at most
	// once, and are admitted without a disposition.
	if !incoming && sndMode == 1 || rcv.Number != 0 || amqpField(p, 7).Code != 0x40 || amqpField(p, 8).Number != 0 || !amqpEmpty(amqpField(p, 12)) {
		return amqpwire.Performative{}, "amqp:not-implemented"
	}
	source, ok := amqpStaticTerminus(amqpField(p, 5), true)
	if !ok {
		return amqpwire.Performative{}, "amqp:not-implemented"
	}
	target, ok := amqpStaticTerminus(amqpField(p, 6), false)
	if !ok {
		return amqpwire.Performative{}, "amqp:not-implemented"
	}
	address := amqpFieldValue(target, 0)
	if !incoming {
		address = amqpFieldValue(source, 0)
	}
	var route *config.AMQPRoute
	for i := range s.routes {
		r := &s.routes[i]
		want := r.RequestAddress
		if !incoming {
			want = r.ResponseAddress
		}
		if address.Code == 0xa1 && string(address.Data) == want {
			route = r
			break
		}
	}
	if route == nil || s.route != nil && route.RequestAddress != s.route.RequestAddress {
		return amqpwire.Performative{}, "amqp:unauthorized-access"
	}
	local := uint32(0)
	for _, l := range s.links {
		if l != nil && l.handle == local {
			local++
		}
	}
	if local > s.negotiation.PeerHandleMax {
		return amqpwire.Performative{}, "amqp:resource-limit-exceeded"
	}
	limit := uint64(amqpwire.MaxFrameBytes)
	if v := amqpField(p, 10); !incoming && v.Code != 0x40 && v.Number != 0 {
		limit = min(limit, v.Number)
	}
	fields := []amqpwire.Value{name, {Code: 0x70, Number: uint64(local)}, {Code: 0x56, Number: 1}, {Code: 0x50, Number: sndMode}, {Code: 0x50}, amqpField(p, 5), amqpTerminus(0x29, route.RequestAddress), {Code: 0x40}, {Code: 0x56}, {Code: 0x40}, {Code: 0x80, Number: amqpwire.MaxFrameBytes}}
	count := uint32(amqpField(p, 9).Number)
	if !incoming {
		fields[2].Number, fields[3].Number = 0, 0
		fields[5], fields[6] = amqpTerminus(0x28, route.ResponseAddress), amqpField(p, 6)
		fields[9] = amqpwire.Value{Code: 0x70}
		count = 0
	}
	reply := amqpwire.Performative{Kind: 0x12, Fields: fields}
	if _, err := amqpwire.EncodePerformative(reply, 0, s.negotiation.PeerFrameLimit); err != nil {
		return amqpwire.Performative{}, "amqp:frame-size-too-small"
	}
	s.route = route
	s.links[local] = &amqpLink{peerHandle: uint32(handle.Number), handle: local, deliveryCount: count, incoming: incoming, messageLimit: limit}
	return reply, ""
}

func amqpField(p amqpwire.Performative, i int) amqpwire.Value { return amqpFieldValue(p.Fields, i) }
func amqpFieldValue(fields []amqpwire.Value, i int) amqpwire.Value {
	if i < len(fields) {
		return fields[i]
	}
	return amqpwire.Value{Code: 0x40}
}
func amqpEmpty(v amqpwire.Value) bool {
	return v.Code == 0x40 || (v.Code == 0xc1 || v.Code == 0xe0) && len(v.Children) == 0
}

func amqpStaticTerminus(v amqpwire.Value, source bool) ([]amqpwire.Value, bool) {
	number, name := uint64(0x29), "amqp:target:list"
	if source {
		number, name = 0x28, "amqp:source:list"
	}
	if v.Code != 0 || v.Descriptor == nil || len(v.Children) != 1 || v.Children[0].Code != 0xc0 || !(v.Descriptor.Code == 0x80 && v.Descriptor.Number == number || v.Descriptor.Code == 0xa3 && string(v.Descriptor.Data) == name) {
		return nil, false
	}
	f := v.Children[0].Children
	if len(amqpFieldValue(f, 0).Data) > 255 || amqpFieldValue(f, 1).Number != 0 || amqpFieldValue(f, 3).Number != 0 || amqpFieldValue(f, 4).Number != 0 || !amqpEmpty(amqpFieldValue(f, 5)) {
		return nil, false
	}
	if expiry := amqpFieldValue(f, 2); expiry.Code != 0x40 && !slices.Contains([]string{"link-detach", "session-end", "connection-close"}, string(expiry.Data)) {
		return nil, false
	}
	if source {
		if mode := amqpFieldValue(f, 6); mode.Code != 0x40 && string(mode.Data) != "move" {
			return nil, false
		}
		if !amqpEmpty(amqpFieldValue(f, 7)) || amqpFieldValue(f, 8).Code != 0x40 || !amqpEmpty(amqpFieldValue(f, 10)) {
			return nil, false
		}
	} else if !amqpEmpty(amqpFieldValue(f, 6)) {
		return nil, false
	}
	return f, true
}

func amqpTerminus(number uint64, address string) amqpwire.Value {
	return amqpwire.Value{Descriptor: &amqpwire.Value{Code: 0x80, Number: number}, Children: []amqpwire.Value{{Code: 0xc0, Children: []amqpwire.Value{{Code: 0xa1, Data: []byte(address)}, {Code: 0x70}, {Code: 0xa3, Data: []byte("link-detach")}}}}}
}
