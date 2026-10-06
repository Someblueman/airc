package bpv7

import "encoding/binary"

// reader handles only the bounded CBOR types used by this ingress profile.
// Integers and lengths must use deterministic shortest-width encodings.
type reader struct {
	data []byte
	pos  int
	err  error
}

func (r *reader) take() byte {
	if r.err != nil || r.pos >= len(r.data) {
		r.err = ErrBundle
		return 0
	}
	value := r.data[r.pos]
	r.pos++
	return value
}

func (r *reader) head(major byte) uint64 {
	header := r.take()
	if r.err != nil || header>>5 != major {
		r.err = ErrBundle
		return 0
	}
	info := header & 31
	if info < 24 {
		return uint64(info)
	}
	var size int
	switch info {
	case 24:
		size = 1
	case 25:
		size = 2
	case 26:
		size = 4
	case 27:
		size = 8
	default:
		r.err = ErrBundle
		return 0
	}
	if size > len(r.data)-r.pos {
		r.err = ErrBundle
		return 0
	}
	body := r.data[r.pos : r.pos+size]
	r.pos += size
	var value uint64
	switch size {
	case 1:
		value = uint64(body[0])
	case 2:
		value = uint64(binary.BigEndian.Uint16(body))
	case 4:
		value = uint64(binary.BigEndian.Uint32(body))
	case 8:
		value = binary.BigEndian.Uint64(body)
	}
	if size == 1 && value < 24 || size == 2 && value <= 255 || size == 4 && value <= 65535 || size == 8 && value <= 4294967295 {
		r.err = ErrBundle
	}
	return value
}

func (r *reader) array(count uint64) bool {
	if r.err == nil && r.pos < len(r.data) && r.data[r.pos] == 0x9f {
		r.pos++
		return true
	}
	if r.head(4) != count {
		r.err = ErrBundle
	}
	return false
}

func (r *reader) end(indefinite bool) {
	if indefinite && r.take() != 0xff {
		r.err = ErrBundle
	}
}

func (r *reader) bytes(major byte, limit uint64) []byte {
	size := r.head(major)
	if r.err != nil || size > limit || size > uint64(len(r.data)-r.pos) {
		r.err = ErrBundle
		return nil
	}
	body := r.data[r.pos : r.pos+int(size)]
	r.pos += int(size)
	return body
}
