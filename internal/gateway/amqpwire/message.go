package amqpwire

const (
	MaxMessageBody     = 32768
	MaxMessageSections = 32
)

// Message keeps transport metadata separate from the opaque signed envelope.
// The session must enforce TTL/expiry, address and publisher policy; properties
// do not substitute for signed request identity or authorization.
type Message struct {
	Body               []byte
	Header, Properties []Value
}

// DecodeMessage accepts format-0 messages with one or more data sections.
// Other body formats are outside the signed-envelope profile. Metadata is
// structurally checked; this terminal ingress does not relay annotations.
func DecodeMessage(wire []byte) (Message, error) {
	if len(wire) == 0 || len(wire) > MaxFrameBytes {
		return Message{}, ErrValue
	}
	var result Message
	last := uint64(0)
	sections := 0
	decoder := valueDecoder{wire: wire, nodes: MaxValueNodes}
	for len(decoder.wire) != 0 {
		sections++
		if sections > MaxMessageSections {
			return Message{}, ErrValue
		}
		section, err := decoder.value(0)
		if err != nil || section.Code != 0 || section.Descriptor == nil || len(section.Children) != 1 {
			return Message{}, ErrValue
		}
		code := messageSectionCode(*section.Descriptor)
		if code == 0 || code < last || code == last && code != 0x75 {
			return Message{}, ErrValue
		}
		body := section.Children[0]
		switch code {
		case 0x70:
			if !messageFields(body, []byte{0x56, 0x50, 0x70, 0x56, 0x70}) {
				return Message{}, ErrValue
			}
			result.Header = body.Children
		case 0x73:
			if !validProperties(body) {
				return Message{}, ErrValue
			}
			result.Properties = body.Children
		case 0x71, 0x72, 0x78:
			if body.Code != 0xc1 {
				return Message{}, ErrValue
			}
			for i := 0; i < len(body.Children); i += 2 {
				if body.Children[i].Code != 0xa3 && body.Children[i].Code != 0x80 {
					return Message{}, ErrValue
				}
				if code == 0x71 && body.Children[i].Code == 0xa3 && string(body.Children[i].Data) == "rejected" && !validMessageError(body.Children[i+1]) {
					return Message{}, ErrValue
				}
			}
		case 0x74:
			if body.Code != 0xc1 {
				return Message{}, ErrValue
			}
			for i := 0; i < len(body.Children); i += 2 {
				value := body.Children[i+1]
				if body.Children[i].Code != 0xa1 || value.Code == 0 || value.Code == 0xc0 || value.Code == 0xc1 || value.Code == 0xe0 {
					return Message{}, ErrValue
				}
			}
		case 0x75:
			if body.Code != 0xa0 || len(body.Data) > MaxMessageBody-len(result.Body) {
				return Message{}, ErrValue
			}
			result.Body = append(result.Body, body.Data...)
		}
		last = code
	}
	if len(result.Body) == 0 {
		return Message{}, ErrValue
	}
	return result, nil
}

func validMessageError(value Value) bool {
	if value.Code != 0 || value.Descriptor == nil || len(value.Children) != 1 {
		return false
	}
	d := value.Descriptor
	if !(d.Code == 0x80 && d.Number == 0x1d || d.Code == 0xa3 && string(d.Data) == "amqp:error:list") {
		return false
	}
	body := value.Children[0]
	if !messageFields(body, []byte{0xa3, 0xa1, 0xc1}) || len(body.Children) == 0 || body.Children[0].Code != 0xa3 {
		return false
	}
	if len(body.Children) == 3 && body.Children[2].Code == 0xc1 {
		for i := 0; i < len(body.Children[2].Children); i += 2 {
			if body.Children[2].Children[i].Code != 0xa3 {
				return false
			}
		}
	}
	return true
}

func EncodeDataMessage(body []byte) ([]byte, error) {
	if len(body) == 0 || len(body) > MaxMessageBody {
		return nil, ErrValue
	}
	return EncodeValue(Value{Descriptor: &Value{Code: 0x80, Number: 0x75}, Children: []Value{{Code: 0xa0, Data: body}}}, MaxFrameBytes)
}

func messageFields(v Value, types []byte) bool {
	if v.Code != 0xc0 || len(v.Children) > len(types) {
		return false
	}
	for i, field := range v.Children {
		if field.Code != 0x40 && field.Code != types[i] {
			return false
		}
	}
	return true
}

func validProperties(v Value) bool {
	types := []byte{0, 0xa0, 0xa1, 0xa1, 0xa1, 0, 0xa3, 0xa3, 0x83, 0x83, 0xa1, 0x70, 0xa1}
	if v.Code != 0xc0 || len(v.Children) > len(types) {
		return false
	}
	for i, field := range v.Children {
		if field.Code == 0x40 {
			continue
		}
		if i == 0 || i == 5 {
			if field.Code != 0x80 && field.Code != 0x98 && field.Code != 0xa0 && field.Code != 0xa1 {
				return false
			}
		} else if field.Code != types[i] {
			return false
		}
	}
	return true
}

func messageSectionCode(descriptor Value) uint64 {
	if descriptor.Code == 0x80 {
		switch descriptor.Number {
		case 0x70, 0x71, 0x72, 0x73, 0x74, 0x75, 0x78:
			return descriptor.Number
		}
	}
	if descriptor.Code == 0xa3 {
		switch string(descriptor.Data) {
		case "amqp:header:list":
			return 0x70
		case "amqp:delivery-annotations:map":
			return 0x71
		case "amqp:message-annotations:map":
			return 0x72
		case "amqp:properties:list":
			return 0x73
		case "amqp:application-properties:map":
			return 0x74
		case "amqp:data:binary":
			return 0x75
		case "amqp:footer:map":
			return 0x78
		}
	}
	return 0
}
