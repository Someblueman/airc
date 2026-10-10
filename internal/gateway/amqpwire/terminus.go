package amqpwire

func describedFields(v Value, number uint64, name string) ([]Value, bool) {
	if v.Code != 0 || v.Descriptor == nil || len(v.Children) != 1 || v.Children[0].Code != 0xc0 {
		return nil, false
	}
	d := v.Descriptor
	if !(d.Code == 0x80 && d.Number == number || d.Code == 0xa3 && string(d.Data) == name) {
		return nil, false
	}
	return v.Children[0].Children, true
}

func validTerminus(v Value, source bool) bool {
	number, name := uint64(0x29), "amqp:target:list"
	types := []byte{0xa1, 0x70, 0xa3, 0x70, 0x56, fieldsType, symbolsType}
	if source {
		number, name = 0x28, "amqp:source:list"
		types = []byte{0xa1, 0x70, 0xa3, 0x70, 0x56, fieldsType, 0xa3, fieldsType, stateType, symbolsType, symbolsType}
	}
	fields, ok := describedFields(v, number, name)
	if !ok || !typedFields(fields, types, 0) {
		return false
	}
	field := func(i int) Value {
		if i < len(fields) {
			return fields[i]
		}
		return Value{Code: 0x40}
	}
	if value := field(1); value.Code != 0x40 && value.Number > 2 {
		return false
	}
	if value := field(2); value.Code != 0x40 {
		switch string(value.Data) {
		case "link-detach", "session-end", "connection-close", "never":
		default:
			return false
		}
	}
	if field(4).Number != 1 && field(5).Code != 0x40 {
		return false
	}
	if source && field(7).Code != 0x40 {
		for i := 1; i < len(field(7).Children); i += 2 {
			value := field(7).Children[i]
			if value.Code != 0x40 && value.Code != 0 {
				return false
			}
		}
	}
	if source && field(8).Code != 0x40 {
		if _, received := describedFields(field(8), 0x23, "amqp:received:list"); received {
			return false
		}
	}
	if source && field(9).Code != 0x40 {
		outcomes := field(9).Children
		if field(9).Code == 0xa3 {
			outcomes = []Value{field(9)}
		}
		for _, outcome := range outcomes {
			switch string(outcome.Data) {
			case "amqp:accepted:list", "amqp:rejected:list", "amqp:released:list", "amqp:modified:list":
			default:
				return false
			}
		}
	}
	return true
}

// Transactional states are outside the selected format-0 ingress profile.
func validDeliveryState(v Value) bool {
	if fields, ok := describedFields(v, 0x23, "amqp:received:list"); ok {
		return typedFields(fields, []byte{0x70, 0x80}, 3)
	}
	if fields, ok := describedFields(v, 0x24, "amqp:accepted:list"); ok {
		return len(fields) == 0
	}
	if fields, ok := describedFields(v, 0x25, "amqp:rejected:list"); ok {
		return typedFields(fields, []byte{errorType}, 0)
	}
	if fields, ok := describedFields(v, 0x26, "amqp:released:list"); ok {
		return len(fields) == 0
	}
	if fields, ok := describedFields(v, 0x27, "amqp:modified:list"); ok {
		return typedFields(fields, []byte{0x56, 0x56, annotationsType}, 0)
	}
	return false
}
