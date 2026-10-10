package amqpwire

// SASLPerformative retains mandatory and optional fields without interpreting
// credentials. Negotiation order, offered mechanisms and TLS are caller policy.
type SASLPerformative struct {
	Kind   byte // 40 mechanisms, 41 init, 42 challenge, 43 response, 44 outcome.
	Fields []Value
}

func DecodeSASL(frame Frame) (SASLPerformative, error) {
	if frame.Type != SASL || frame.Channel != 0 || len(frame.Body) == 0 || len(frame.Body) > InitialFrameLimit-8 {
		return SASLPerformative{}, ErrValue
	}
	v, n, err := DecodeValue(frame.Body)
	if err != nil || n != len(frame.Body) || v.Code != 0 || v.Descriptor == nil || len(v.Children) != 1 || v.Children[0].Code != 0xc0 {
		return SASLPerformative{}, ErrValue
	}
	kind := saslDescriptor(*v.Descriptor)
	p := SASLPerformative{Kind: kind, Fields: v.Children[0].Children}
	if !validSASL(p) {
		return SASLPerformative{}, ErrValue
	}
	return p, nil
}

func EncodeSASL(p SASLPerformative) (Frame, error) {
	if !validSASL(p) {
		return Frame{}, ErrValue
	}
	body, err := EncodeValue(Value{Descriptor: &Value{Code: 0x80, Number: uint64(p.Kind)}, Children: []Value{{Code: 0xc0, Children: p.Fields}}}, InitialFrameLimit-8)
	if err != nil {
		return Frame{}, err
	}
	return Frame{Type: SASL, Body: body}, nil
}

func validSASL(p SASLPerformative) bool {
	if len(p.Fields) == 0 || p.Fields[0].Code == 0x40 {
		return false
	}
	var types []byte
	switch p.Kind {
	case 0x40:
		if len(p.Fields) != 1 {
			return false
		}
		v := p.Fields[0]
		if v.Code == 0xa3 {
			return len(v.Data) != 0
		}
		if v.Code != 0xe0 || len(v.Children) == 0 {
			return false
		}
		for _, mechanism := range v.Children {
			if mechanism.Code != 0xa3 || len(mechanism.Data) == 0 {
				return false
			}
		}
		return true
	case 0x41:
		types = []byte{0xa3, 0xa0, 0xa1}
		if len(p.Fields[0].Data) == 0 {
			return false
		}
	case 0x42, 0x43:
		types = []byte{0xa0}
	case 0x44:
		types = []byte{0x50, 0xa0}
		if p.Fields[0].Number > 4 || p.Fields[0].Number != 0 && len(p.Fields) > 1 && p.Fields[1].Code != 0x40 {
			return false
		}
	default:
		return false
	}
	if len(p.Fields) > len(types) {
		return false
	}
	for i, v := range p.Fields {
		if v.Code != types[i] && !(i > 0 && v.Code == 0x40) {
			return false
		}
	}
	return true
}

func saslDescriptor(v Value) byte {
	if v.Code == 0x80 && v.Number >= 0x40 && v.Number <= 0x44 {
		return byte(v.Number)
	}
	if v.Code == 0xa3 {
		switch string(v.Data) {
		case "amqp:sasl-mechanisms:list":
			return 0x40
		case "amqp:sasl-init:list":
			return 0x41
		case "amqp:sasl-challenge:list":
			return 0x42
		case "amqp:sasl-response:list":
			return 0x43
		case "amqp:sasl-outcome:list":
			return 0x44
		}
	}
	return 0
}
