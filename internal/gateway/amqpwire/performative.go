package amqpwire

// Performative retains typed fields and a transfer's opaque payload. Kind zero
// denotes a heartbeat; channel validity and state ordering belong to the session.
type Performative struct {
	Kind    byte
	Fields  []Value
	Payload []byte
}

func DecodePerformative(frame Frame) (Performative, error) {
	if frame.Type != AMQP || len(frame.Body) > MaxFrameBytes-8 {
		return Performative{}, ErrValue
	}
	if len(frame.Body) == 0 {
		return Performative{}, nil
	}
	v, n, err := DecodeValue(frame.Body)
	if err != nil || v.Code != 0 || v.Descriptor == nil || len(v.Children) != 1 || v.Children[0].Code != 0xc0 {
		return Performative{}, ErrValue
	}
	kind := transportDescriptor(*v.Descriptor)
	p := Performative{Kind: kind, Fields: v.Children[0].Children}
	if !validPerformative(p) || kind == 0x10 && frame.Channel != 0 || kind != 0x14 && n != len(frame.Body) {
		return Performative{}, ErrValue
	}
	p.Payload = append([]byte(nil), frame.Body[n:]...)
	return p, nil
}

func EncodePerformative(p Performative, channel uint16, limit uint32) (Frame, error) {
	if !validLimit(limit) || len(p.Payload) > int(limit)-8 || p.Kind == 0x10 && channel != 0 {
		return Frame{}, ErrValue
	}
	if p.Kind == 0 && len(p.Fields) == 0 && len(p.Payload) == 0 {
		return Frame{Type: AMQP, Channel: channel}, nil
	}
	if !validPerformative(p) || p.Kind != 0x14 && len(p.Payload) != 0 {
		return Frame{}, ErrValue
	}
	body, err := EncodeValue(Value{Descriptor: &Value{Code: 0x80, Number: uint64(p.Kind)}, Children: []Value{{Code: 0xc0, Children: p.Fields}}}, int(limit)-8-len(p.Payload))
	if err != nil {
		return Frame{}, err
	}
	body = append(body, p.Payload...)
	return Frame{Type: AMQP, Channel: channel, Body: body}, nil
}

const (
	symbolsType     = byte(0xf1)
	fieldsType      = byte(0xf2)
	sourceType      = byte(0xf3)
	targetType      = byte(0xf4)
	unsettledType   = byte(0xf5)
	stateType       = byte(0xf6)
	errorType       = byte(0xf7)
	annotationsType = byte(0xf8)
)

func validPerformative(p Performative) bool {
	var types []byte
	var mandatory uint16
	switch p.Kind {
	case 0x10:
		types = []byte{0xa1, 0xa1, 0x70, 0x60, 0x70, symbolsType, symbolsType, symbolsType, symbolsType, fieldsType}
		mandatory = 1
	case 0x11:
		types = []byte{0x60, 0x70, 0x70, 0x70, 0x70, symbolsType, symbolsType, fieldsType}
		mandatory = 14
	case 0x12:
		types = []byte{0xa1, 0x70, 0x56, 0x50, 0x50, sourceType, targetType, unsettledType, 0x56, 0x70, 0x80, symbolsType, symbolsType, fieldsType}
		mandatory = 7
	case 0x13:
		types = []byte{0x70, 0x70, 0x70, 0x70, 0x70, 0x70, 0x70, 0x70, 0x56, 0x56, fieldsType}
		mandatory = 14
	case 0x14:
		types = []byte{0x70, 0x70, 0xa0, 0x70, 0x56, 0x56, 0x50, stateType, 0x56, 0x56, 0x56}
		mandatory = 1
	case 0x15:
		types = []byte{0x56, 0x70, 0x70, 0x56, stateType, 0x56}
		mandatory = 3
	case 0x16:
		types = []byte{0x70, 0x56, errorType}
		mandatory = 1
	case 0x17, 0x18:
		types = []byte{errorType}
	default:
		return false
	}
	if !typedFields(p.Fields, types, mandatory) {
		return false
	}
	field := func(i int) Value {
		if i < len(p.Fields) {
			return p.Fields[i]
		}
		return Value{Code: 0x40}
	}
	switch p.Kind {
	case 0x10:
		if v := field(2); v.Code != 0x40 && v.Number < InitialFrameLimit {
			return false
		}
	case 0x12:
		if field(2).Number == 0 && field(9).Code == 0x40 {
			return false
		}
		if v := field(3); v.Code != 0x40 && v.Number > 2 {
			return false
		}
		if v := field(4); v.Code != 0x40 && v.Number > 1 {
			return false
		}
	case 0x13:
		if field(4).Code == 0x40 && (field(5).Code != 0x40 || field(6).Code != 0x40 || field(7).Code != 0x40) {
			return false
		}
	case 0x14:
		if v := field(2); v.Code != 0x40 && len(v.Data) > 32 {
			return false
		}
		if v := field(3); v.Code != 0x40 && v.Number != 0 {
			return false
		}
		if v := field(6); v.Code != 0x40 && v.Number > 1 {
			return false
		}
	}
	return true
}

func typedFields(fields []Value, types []byte, mandatory uint16) bool {
	if len(fields) > len(types) {
		return false
	}
	for i, kind := range types {
		v := Value{Code: 0x40}
		if i < len(fields) {
			v = fields[i]
		}
		if v.Code == 0x40 {
			if mandatory&(1<<i) != 0 {
				return false
			}
			continue
		}
		if !typedField(v, kind) {
			return false
		}
	}
	return true
}

func typedField(v Value, kind byte) bool {
	switch kind {
	case symbolsType:
		if v.Code == 0xa3 {
			return true
		}
		if v.Code != 0xe0 {
			return false
		}
		for _, s := range v.Children {
			if s.Code != 0xa3 {
				return false
			}
		}
		return true
	case fieldsType:
		if v.Code != 0xc1 {
			return false
		}
		for i := 0; i < len(v.Children); i += 2 {
			if v.Children[i].Code != 0xa3 {
				return false
			}
		}
		return true
	case annotationsType:
		if v.Code != 0xc1 || len(v.Children)%2 != 0 {
			return false
		}
		for i := 0; i < len(v.Children); i += 2 {
			if v.Children[i].Code != 0xa3 && v.Children[i].Code != 0x80 {
				return false
			}
		}
		return true
	case sourceType, targetType:
		return validTerminus(v, kind == sourceType)
	case stateType:
		return validDeliveryState(v)
	case unsettledType:
		if v.Code != 0xc1 || len(v.Children)%2 != 0 {
			return false
		}
		for i := 0; i < len(v.Children); i += 2 {
			if v.Children[i].Code != 0xa0 || len(v.Children[i].Data) > 32 || v.Children[i+1].Code != 0x40 && !validDeliveryState(v.Children[i+1]) {
				return false
			}
		}
		return true
	case errorType:
		return validMessageError(v)
	}
	return v.Code == kind
}

func transportDescriptor(v Value) byte {
	if v.Code == 0x80 && v.Number >= 0x10 && v.Number <= 0x18 {
		return byte(v.Number)
	}
	if v.Code == 0xa3 {
		switch string(v.Data) {
		case "amqp:open:list":
			return 0x10
		case "amqp:begin:list":
			return 0x11
		case "amqp:attach:list":
			return 0x12
		case "amqp:flow:list":
			return 0x13
		case "amqp:transfer:list":
			return 0x14
		case "amqp:disposition:list":
			return 0x15
		case "amqp:detach:list":
			return 0x16
		case "amqp:end:list":
			return 0x17
		case "amqp:close:list":
			return 0x18
		}
	}
	return 0
}
