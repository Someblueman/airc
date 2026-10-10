package snmpwire

func parseVariable(name string, tag byte, value reader) (Variable, bool) {
	v := Variable{OID: name, Type: tag}
	// Decode original primitive contents; field authority and rendering are separate.
	p := value.bytes()
	switch tag {
	case 4:
		if len(p) > 512 {
			return Variable{}, false
		}
		v.Bytes = p
	case 6:
		var ok bool
		v.ObjectID, ok = decodeOID(p)
		if !ok {
			return Variable{}, false
		}
	case 2:
		if !minimalInteger(p) || len(p) > 4 {
			return Variable{}, false
		}
		n := int32(int8(p[0]))
		for _, b := range p[1:] {
			n = n<<8 | int32(b)
		}
		v.Integer = int64(n)
	case 64:
		if len(p) != 4 {
			return Variable{}, false
		}
		v.Bytes = p
	case 65, 66, 67, 70:
		if !minimalInteger(p) || p[0]&128 != 0 || len(p) > 9 {
			return Variable{}, false
		}
		bits := 32
		if tag == 70 {
			bits = 64
		}
		for _, b := range p {
			if v.Unsigned > (^uint64(0)-uint64(b))/256 {
				return Variable{}, false
			}
			v.Unsigned = v.Unsigned*256 + uint64(b)
		}
		if bits == 32 && v.Unsigned > 4294967295 {
			return Variable{}, false
		}
	default:
		return Variable{}, false
	}
	return v, true
}
