package amqpwire

import "encoding/binary"

// EncodeValue validates the selected protocol-field profile before allocating
// output. The limit includes the entire encoded value, not an enclosing frame.
// Caller-owned values must remain unchanged during encoding.
func EncodeValue(value Value, limit int) ([]byte, error) {
	if limit < 1 || limit > MaxFrameBytes {
		return nil, ErrValue
	}
	check := valueEncoder{limit: limit, nodes: MaxValueNodes}
	if err := check.value(value, 0); err != nil {
		return nil, err
	}
	encoder := valueEncoder{limit: limit, nodes: MaxValueNodes, output: make([]byte, 0, check.size)}
	if err := encoder.value(value, 0); err != nil {
		clear(encoder.output)
		return nil, err
	}
	return encoder.output, nil
}

type valueEncoder struct {
	limit, size, nodes int
	output             []byte
}

func (e *valueEncoder) add(data []byte) error {
	if len(data) > e.limit-e.size {
		return ErrValue
	}
	e.size += len(data)
	if e.output != nil {
		e.output = append(e.output, data...)
	}
	return nil
}

func (e *valueEncoder) integer(number uint64, width int) error {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], number)
	return e.add(encoded[8-width:])
}

func (e *valueEncoder) value(v Value, depth int) error {
	if depth >= MaxValueDepth || e.nodes == 0 {
		return ErrValue
	}
	e.nodes--
	collection := v.Code == 0xc0 || v.Code == 0xc1 || v.Code == 0xe0
	text := v.Code == 0xa0 || v.Code == 0xa1 || v.Code == 0xa3
	numeric := v.Code == 0x56 || v.Code == 0x50 || v.Code == 0x60 || v.Code == 0x70 || v.Code == 0x80
	if (!collection && v.Code != 0 && len(v.Children) != 0) ||
		(v.Code != 0 && v.Descriptor != nil) || (!text && len(v.Data) != 0) ||
		(!numeric && v.Number != 0) {
		return ErrValue
	}
	if v.Code == 0 {
		if v.Descriptor == nil || len(v.Children) != 1 || (v.Descriptor.Code != 0x80 && v.Descriptor.Code != 0xa3) {
			return ErrValue
		}
		if err := e.add([]byte{0}); err != nil {
			return err
		}
		if err := e.value(*v.Descriptor, depth+1); err != nil {
			return err
		}
		return e.value(v.Children[0], depth+1)
	}
	if collection {
		return e.collection(v, depth)
	}
	width := 0
	switch v.Code {
	case 0x40:
	case 0x56:
		if v.Number > 1 {
			return ErrValue
		}
		width = 1
	case 0x50:
		if v.Number > 255 {
			return ErrValue
		}
		width = 1
	case 0x60:
		if v.Number > 65535 {
			return ErrValue
		}
		width = 2
	case 0x70:
		if v.Number > 0xffffffff {
			return ErrValue
		}
		width = 4
	case 0x80:
		width = 8
	case 0xa0, 0xa1, 0xa3:
		if len(v.Data) > e.limit || !validText(v.Code, v.Data) {
			return ErrValue
		}
		if err := e.add([]byte{v.Code + 0x10}); err != nil {
			return err
		}
		if err := e.integer(uint64(len(v.Data)), 4); err != nil {
			return err
		}
		return e.add(v.Data)
	default:
		return ErrValue
	}
	if err := e.add([]byte{v.Code}); err != nil {
		return err
	}
	return e.integer(v.Number, width)
}

func (e *valueEncoder) collection(v Value, depth int) error {
	count := len(v.Children)
	if count > MaxValueCount || count > e.nodes || v.Code == 0xc1 && count%2 != 0 {
		return ErrValue
	}
	start := e.size
	code := v.Code + 0x10
	if err := e.add([]byte{code, 0, 0, 0, 0}); err != nil {
		return err
	}
	if err := e.integer(uint64(count), 4); err != nil {
		return err
	}
	if v.Code == 0xe0 {
		if err := e.add([]byte{0xb3}); err != nil {
			return err
		}
	}
	type key struct {
		code   byte
		number uint64
		data   string
	}
	keys := make(map[key]bool)
	for i, child := range v.Children {
		if v.Code == 0xe0 {
			if child.Code != 0xa3 || len(child.Children) != 0 || child.Descriptor != nil || child.Number != 0 ||
				len(child.Data) > e.limit || !validText(0xa3, child.Data) || depth+1 >= MaxValueDepth || e.nodes == 0 {
				return ErrValue
			}
			e.nodes--
			if err := e.integer(uint64(len(child.Data)), 4); err != nil {
				return err
			}
			if err := e.add(child.Data); err != nil {
				return err
			}
		} else {
			if err := e.value(child, depth+1); err != nil {
				return err
			}
		}
		if v.Code == 0xc1 && i%2 == 0 {
			if child.Code == 0 || child.Code == 0x40 || child.Code == 0xc0 || child.Code == 0xc1 || child.Code == 0xe0 {
				return ErrValue
			}
			k := key{child.Code, child.Number, string(child.Data)}
			if keys[k] {
				return ErrValue
			}
			keys[k] = true
		}
	}
	if e.output != nil {
		binary.BigEndian.PutUint32(e.output[start+1:start+5], uint32(e.size-start-5))
	}
	return nil
}
