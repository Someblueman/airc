package adapter

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/amqpwire"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func amqpLinkFixture() amqpLinks {
	return amqpLinks{negotiation: amqpwire.Negotiation{PeerFrameLimit: 512, PeerHandleMax: 1}, routes: []config.AMQPRoute{{RequestAddress: "request", ResponseAddress: "response", Target: core.Target{Kind: "room", Name: "#fixture"}, Operations: []core.Operation{core.Send}}}}
}

func amqpAttach(incoming bool, handle uint32, address string) amqpwire.Performative {
	p := amqpwire.Performative{Kind: 0x12, Fields: []amqpwire.Value{{Code: 0xa1, Data: []byte("request-link")}, {Code: 0x70, Number: uint64(handle)}, {Code: 0x56}, {Code: 0x50}, {Code: 0x50}, amqpTerminus(0x28, ""), amqpTerminus(0x29, address), {Code: 0x40}, {Code: 0x56}, {Code: 0x70, Number: 7}}}
	if !incoming {
		p.Fields[0].Data = []byte("response-link")
		p.Fields[2].Number = 1
		p.Fields[3].Number = 2
		p.Fields[5], p.Fields[6] = amqpTerminus(0x28, address), amqpTerminus(0x29, "")
		p.Fields[9] = amqpwire.Value{Code: 0x40}
	}
	return p
}

func TestAMQPLinkPairEitherOrder(t *testing.T) {
	for _, firstIncoming := range []bool{true, false} {
		s := amqpLinkFixture()
		for i, incoming := range []bool{firstIncoming, !firstIncoming} {
			address := "request"
			if !incoming {
				address = "response"
			}
			p := amqpAttach(incoming, uint32(1-i), address)
			reply, condition := s.attach(p)
			if condition != "" || reply.Fields[1].Number != uint64(i) || reply.Fields[2].Number == p.Fields[2].Number || reply.Fields[3].Number != 0 || reply.Fields[4].Number != 0 {
				t.Fatal(reply, condition)
			}
			frame, err := amqpwire.EncodePerformative(reply, 0, 512)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := amqpwire.DecodePerformative(frame); err != nil {
				t.Fatal(err)
			}
			l := s.links[i]
			if l == nil || l.incoming != incoming || l.peerHandle != uint32(1-i) || l.messageLimit != amqpwire.MaxFrameBytes {
				t.Fatal(l)
			}
			if incoming && l.deliveryCount != 7 || !incoming && (l.deliveryCount != 0 || reply.Fields[9].Code != 0x70) {
				t.Fatal("wrong initial link counter")
			}
		}
		if s.route == nil || s.route.Target.Name != "#fixture" {
			t.Fatal("pair not bound to exact grant")
		}
	}
}

func TestAMQPAttachIndependentBytes(t *testing.T) {
	body, err := hex.DecodeString("005312c0240aa1037265715201425000500000532845005329c00a01a1077265717565737440425207")
	if err != nil {
		t.Fatal(err)
	}
	p, err := amqpwire.DecodePerformative(amqpwire.Frame{Type: amqpwire.AMQP, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	s := amqpLinkFixture()
	reply, condition := s.attach(p)
	if condition != "" || string(reply.Fields[0].Data) != "req" || s.links[0].peerHandle != 1 || s.links[0].deliveryCount != 7 {
		t.Fatal(reply, condition)
	}
}

func TestAMQPLinkAuthorityAndHandleRefusals(t *testing.T) {
	s := amqpLinkFixture()
	if _, condition := s.attach(amqpAttach(true, 1, "request")); condition != "" {
		t.Fatal(condition)
	}
	for _, test := range []struct {
		p    amqpwire.Performative
		want string
	}{
		{amqpAttach(false, 1, "response"), "amqp:session:handle-in-use"},
		{amqpAttach(false, 2, "response"), "amqp:connection:framing-error"},
		{amqpAttach(true, 0, "request"), "amqp:resource-limit-exceeded"},
		{amqpAttach(false, 0, "unknown"), "amqp:unauthorized-access"},
	} {
		if _, condition := s.attach(test.p); condition != test.want || s.links[1] != nil {
			t.Fatal(condition, test.want)
		}
	}
	s.negotiation.PeerHandleMax = 0
	if _, condition := s.attach(amqpAttach(false, 0, "response")); condition != "amqp:resource-limit-exceeded" {
		t.Fatal(condition)
	}
	s = amqpLinkFixture()
	second := s.routes[0]
	second.RequestAddress, second.ResponseAddress = "request2", "response2"
	s.routes = append(s.routes, second)
	if _, condition := s.attach(amqpAttach(false, 0, "response")); condition != "" {
		t.Fatal(condition)
	}
	if _, condition := s.attach(amqpAttach(true, 1, "request2")); condition != "amqp:unauthorized-access" || s.links[1] != nil {
		t.Fatal("mixed grants", condition)
	}
}

func TestAMQPAttachReplyFrameBudget(t *testing.T) {
	s := amqpLinkFixture()
	s.routes[0].RequestAddress = strings.Repeat("a", 255)
	p := amqpAttach(true, 0, s.routes[0].RequestAddress)
	p.Fields[0].Data = []byte(strings.Repeat("n", 128))
	p.Fields[5] = amqpTerminus(0x28, strings.Repeat("s", 255))
	if _, condition := s.attach(p); condition != "amqp:frame-size-too-small" || s.route != nil || s.links[0] != nil {
		t.Fatal(condition)
	}
	s.negotiation.PeerFrameLimit = 1024
	if _, condition := s.attach(p); condition != "" {
		t.Fatal(condition)
	}
}
