package adapter

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/amqpwire"
)

func amqpTerminusField(v *amqpwire.Value, index int, field amqpwire.Value) {
	f := &v.Children[0].Children
	for len(*f) <= index {
		*f = append(*f, amqpwire.Value{Code: 0x40})
	}
	(*f)[index] = field
}

func TestAMQPStaticLinkProfileRefusals(t *testing.T) {
	for name, mutate := range map[string]func(*amqpwire.Performative){
		"settled_sender":    func(p *amqpwire.Performative) { p.Fields[3].Number = 1 },
		"mixed_sender":      func(p *amqpwire.Performative) { p.Fields[3].Number = 2 },
		"receiver_second":   func(p *amqpwire.Performative) { p.Fields[4].Number = 1 },
		"unsettled_resume":  func(p *amqpwire.Performative) { p.Fields[7] = amqpwire.Value{Code: 0xc1} },
		"incomplete_resume": func(p *amqpwire.Performative) { p.Fields[8].Number = 1 },
		"dynamic": func(p *amqpwire.Performative) {
			amqpTerminusField(&p.Fields[6], 4, amqpwire.Value{Code: 0x56, Number: 1})
		},
		"durable": func(p *amqpwire.Performative) {
			amqpTerminusField(&p.Fields[6], 1, amqpwire.Value{Code: 0x70, Number: 1})
		},
		"timeout": func(p *amqpwire.Performative) {
			amqpTerminusField(&p.Fields[6], 3, amqpwire.Value{Code: 0x70, Number: 1})
		},
		"never_expiry": func(p *amqpwire.Performative) {
			amqpTerminusField(&p.Fields[6], 2, amqpwire.Value{Code: 0xa3, Data: []byte("never")})
		},
		"copy": func(p *amqpwire.Performative) {
			amqpTerminusField(&p.Fields[5], 6, amqpwire.Value{Code: 0xa3, Data: []byte("copy")})
		},
		"filter": func(p *amqpwire.Performative) {
			amqpTerminusField(&p.Fields[5], 7, amqpwire.Value{Code: 0xc1, Children: []amqpwire.Value{{Code: 0xa3, Data: []byte("selector")}, {Descriptor: &amqpwire.Value{Code: 0x80, Number: 0x99}, Children: []amqpwire.Value{{Code: 0x40}}}}})
		},
		"default_outcome": func(p *amqpwire.Performative) {
			amqpTerminusField(&p.Fields[5], 8, amqpwire.Value{Descriptor: &amqpwire.Value{Code: 0x80, Number: 0x24}, Children: []amqpwire.Value{{Code: 0xc0}}})
		},
		"target_capability": func(p *amqpwire.Performative) {
			amqpTerminusField(&p.Fields[6], 6, amqpwire.Value{Code: 0xa3, Data: []byte("queue")})
		},
		"desired_capability": func(p *amqpwire.Performative) {
			for len(p.Fields) < 13 {
				p.Fields = append(p.Fields, amqpwire.Value{Code: 0x40})
			}
			p.Fields[12] = amqpwire.Value{Code: 0xa3, Data: []byte("queue")}
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := amqpLinkFixture()
			p := amqpAttach(true, 0, "request")
			mutate(&p)
			frame, err := amqpwire.EncodePerformative(p, 0, amqpwire.MaxFrameBytes)
			if err != nil {
				t.Fatal("test must be valid AMQP codec input", err)
			}
			p, err = amqpwire.DecodePerformative(frame)
			if err != nil {
				t.Fatal(err)
			}
			if _, condition := s.attach(p); condition != "amqp:not-implemented" || s.route != nil || s.links[0] != nil {
				t.Fatal("unsupported semantics accepted", condition)
			}
		})
	}
}

func TestAMQPSymbolicTerminiAndResponseLimit(t *testing.T) {
	s := amqpLinkFixture()
	p := amqpAttach(false, 0, "response")
	p.Fields[5].Descriptor = &amqpwire.Value{Code: 0xa3, Data: []byte("amqp:source:list")}
	p.Fields[6].Descriptor = &amqpwire.Value{Code: 0xa3, Data: []byte("amqp:target:list")}
	p.Fields = append(p.Fields, amqpwire.Value{Code: 0x80, Number: 20})
	if _, condition := s.attach(p); condition != "" || s.links[0].messageLimit != 20 {
		t.Fatal(condition, s.links[0])
	}
}
