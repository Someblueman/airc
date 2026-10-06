package amqpwire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"unicode/utf8"
)

const (
	MaxValueDepth = 16
	MaxValueCount = 1024
	MaxValueNodes = 2048
)

var ErrValue = errors.New("invalid or unsupported AMQP protocol-field value")

// Value retains AMQP type distinctions. Code is the canonical constructor:
// null=40, boolean=56, ubyte=50, ushort=60, uint=70, ulong=80, binary=a0,
// string=a1, symbol=a3, timestamp=83 (raw signed bits), UUID=98, list=c0,
// map=c1, symbol-array=e0, described=00.
// Data is owned. Maps retain alternating entries for later field validation.
type Value struct {
	Code       byte
	Number     uint64
	Data       []byte
	Children   []Value
	Descriptor *Value
}

// DecodeValue consumes one bounded protocol-field value and returns its byte
// count. Trailing bytes belong to the caller. Only symbol arrays are supported;
// reserved descriptors and primitive types outside this profile are refused.
func DecodeValue(wire []byte) (Value, int, error) {
	if len(wire) == 0 || len(wire) > MaxFrameBytes {
		return Value{}, 0, ErrValue
	}
	d := valueDecoder{wire: wire, nodes: MaxValueNodes}
	v, err := d.value(0)
	if err != nil {
		return Value{}, 0, err
	}
	return v, len(wire) - len(d.wire), nil
}

type valueDecoder struct {
	wire  []byte
	nodes int
}

func (d *valueDecoder) take(n int) ([]byte, error) {
	if n < 0 || n > len(d.wire) {
		return nil, ErrValue
	}
	b := d.wire[:n]
	d.wire = d.wire[n:]
	return b, nil
}

func (d *valueDecoder) number(width int) (uint64, error) {
	b, err := d.take(width)
	if err != nil {
		return 0, err
	}
	var n uint64
	for _, octet := range b {
		n = n<<8 | uint64(octet)
	}
	return n, nil
}

func (d *valueDecoder) value(depth int) (Value, error) {
	if depth >= MaxValueDepth || d.nodes == 0 {
		return Value{}, ErrValue
	}
	d.nodes--
	b, err := d.take(1)
	if err != nil {
		return Value{}, err
	}
	code := b[0]
	v := Value{Code: code}
	switch code {
	case 0x00:
		descriptor, err := d.value(depth + 1)
		if err != nil || (descriptor.Code != 0x80 && descriptor.Code != 0xa3) {
			return Value{}, ErrValue
		}
		body, err := d.value(depth + 1)
		if err != nil {
			return Value{}, err
		}
		v.Descriptor, v.Children = &descriptor, []Value{body}
	case 0x40:
	case 0x41, 0x42:
		v.Code = 0x56
		if code == 0x41 {
			v.Number = 1
		}
	case 0x43, 0x44:
		v.Code = 0x70
		if code == 0x44 {
			v.Code = 0x80
		}
	case 0x45:
		v.Code = 0xc0
	case 0x50, 0x52, 0x53, 0x56, 0x60, 0x70, 0x80, 0x83:
		width := 1
		switch code {
		case 0x52:
			v.Code = 0x70
		case 0x53:
			v.Code = 0x80
		case 0x60:
			width = 2
		case 0x70:
			width = 4
		case 0x80, 0x83:
			width = 8
		}
		v.Number, err = d.number(width)
		if code == 0x56 && v.Number > 1 {
			return Value{}, ErrValue
		}
	case 0x98:
		var data []byte
		data, err = d.take(16)
		v.Data = append([]byte(nil), data...)
	case 0xa0, 0xa1, 0xa3, 0xb0, 0xb1, 0xb3:
		width := 1
		if code >= 0xb0 {
			width, v.Code = 4, code-0x10
		}
		var size uint64
		size, err = d.number(width)
		if err != nil || size > uint64(len(d.wire)) {
			return Value{}, ErrValue
		}
		var data []byte
		data, err = d.take(int(size))
		if !validText(v.Code, data) {
			return Value{}, ErrValue
		}
		v.Data = append([]byte(nil), data...)
	case 0xc0, 0xc1, 0xd0, 0xd1, 0xe0, 0xf0:
		return d.collection(code, depth)
	default:
		return Value{}, ErrValue
	}
	return v, err
}

func validText(code byte, data []byte) bool {
	if code == 0xa1 {
		return utf8.Valid(data) && !bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf})
	}
	if code == 0xa3 {
		for _, octet := range data {
			if octet > 127 {
				return false
			}
		}
	}
	return true
}

func (d *valueDecoder) collection(code byte, depth int) (Value, error) {
	width := 1
	if code == 0xd0 || code == 0xd1 || code == 0xf0 {
		width = 4
	}
	size, err := d.number(width)
	if err != nil || size < uint64(width) || size > uint64(len(d.wire)) {
		return Value{}, ErrValue
	}
	payload, _ := d.take(int(size))
	count := uint64(payload[0])
	if width == 4 {
		count = uint64(binary.BigEndian.Uint32(payload[:4]))
	}
	array := code == 0xe0 || code == 0xf0
	mapValue := code == 0xc1 || code == 0xd1
	if count > MaxValueCount || count > uint64(d.nodes) || mapValue && count%2 != 0 {
		return Value{}, ErrValue
	}
	child := valueDecoder{wire: payload[width:], nodes: d.nodes}
	constructor := byte(0)
	if array {
		b, err := child.take(1)
		if err != nil || (b[0] != 0xa3 && b[0] != 0xb3) {
			return Value{}, ErrValue
		}
		constructor = b[0]
	}
	// Every supported element requires at least one octet, including an array
	// symbol's length. Check this before allocating collection storage.
	if count > uint64(len(child.wire)) {
		return Value{}, ErrValue
	}
	v := Value{Code: 0xc0, Children: make([]Value, 0, int(count))}
	if mapValue {
		v.Code = 0xc1
	} else if array {
		v.Code = 0xe0
	}
	type mapKey struct {
		code   byte
		number uint64
		data   string
	}
	keys := make(map[mapKey]bool)
	for i := uint64(0); i < count; i++ {
		var element Value
		var err error
		if array {
			if depth+1 >= MaxValueDepth || child.nodes == 0 {
				return Value{}, ErrValue
			}
			child.nodes--
			width := 1
			if constructor == 0xb3 {
				width = 4
			}
			size, e := child.number(width)
			if e != nil || size > uint64(len(child.wire)) {
				return Value{}, ErrValue
			}
			data, _ := child.take(int(size))
			if !validText(0xa3, data) {
				return Value{}, ErrValue
			}
			element = Value{Code: 0xa3, Data: append([]byte(nil), data...)}
		} else {
			element, err = child.value(depth + 1)
		}
		if err != nil {
			return Value{}, err
		}
		if mapValue && i%2 == 0 {
			if element.Code == 0x40 || element.Code == 0 || len(element.Children) != 0 || element.Code == 0xc0 || element.Code == 0xc1 || element.Code == 0xe0 {
				return Value{}, ErrValue
			}
			key := mapKey{element.Code, element.Number, string(element.Data)}
			if keys[key] {
				return Value{}, ErrValue
			}
			keys[key] = true
		}
		v.Children = append(v.Children, element)
	}
	if len(child.wire) != 0 {
		return Value{}, ErrValue
	}
	d.nodes = child.nodes
	return v, nil
}
