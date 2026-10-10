package snmpwire

import (
	"math"
	"strconv"
	"strings"
)

// A reader walks only fixed protocol nesting, retaining offsets into owned bytes.
// RFC 3417 permits nonminimal definite lengths, but forbids indefinite lengths.
type reader struct {
	raw      []byte
	pos, end int
}

func (r *reader) next() (byte, reader, bool) {
	if r.pos+2 > r.end {
		return 0, reader{}, false
	}
	tag := r.raw[r.pos]
	r.pos++
	if tag&31 == 31 {
		return 0, reader{}, false
	}
	size := int(r.raw[r.pos])
	r.pos++
	if size&128 != 0 {
		n := size & 127
		if n == 0 || n > 4 || n > r.end-r.pos {
			return 0, reader{}, false
		}
		size = 0
		for range n {
			if size > MaxPacket/256 {
				return 0, reader{}, false
			}
			size = size*256 + int(r.raw[r.pos])
			if size > MaxPacket {
				return 0, reader{}, false
			}
			r.pos++
		}
	}
	if size > r.end-r.pos {
		return 0, reader{}, false
	}
	body := reader{r.raw, r.pos, r.pos + size}
	r.pos += size
	return tag, body, true
}
func (r *reader) child(want byte) (reader, bool) {
	tag, body, ok := r.next()
	return body, ok && tag == want
}
func (r reader) bytes() []byte { return r.raw[r.pos:r.end] }
func (r reader) done() bool    { return r.pos == r.end }
func (r *reader) octets(max int) ([]byte, bool) {
	v, ok := r.child(4)
	return v.bytes(), ok && v.end-v.pos <= max
}
func minimalInteger(p []byte) bool {
	return len(p) > 0 && (len(p) == 1 || !(p[0] == 0 && p[1]&128 == 0 || p[0] == 255 && p[1]&128 != 0))
}
func (r *reader) signed() (int32, bool) {
	v, ok := r.child(2)
	p := v.bytes()
	if !ok || !minimalInteger(p) || len(p) > 4 {
		return 0, false
	}
	n := int32(int8(p[0]))
	for _, b := range p[1:] {
		n = n<<8 | int32(b)
	}
	return n, true
}
func (r *reader) unsigned(tag byte, bits int) (uint64, bool) {
	v, ok := r.child(tag)
	p := v.bytes()
	if !ok || !minimalInteger(p) || p[0]&128 != 0 || len(p) > 9 {
		return 0, false
	}
	var n uint64
	for _, b := range p {
		if n > (math.MaxUint64-uint64(b))/256 {
			return 0, false
		}
		n = n*256 + uint64(b)
	}
	return n, bits == 64 || n < (uint64(1)<<bits)
}
func (r *reader) oid() (string, bool) {
	v, ok := r.child(6)
	if !ok {
		return "", false
	}
	return decodeOID(v.bytes())
}
func decodeOID(p []byte) (string, bool) {
	if len(p) == 0 || len(p) > 640 {
		return "", false
	}
	var arcs []uint64
	for len(p) > 0 {
		if p[0] == 128 {
			return "", false
		}
		var n uint64
		for {
			if len(p) == 0 || n > (uint64(math.MaxUint32)+80)>>7 {
				return "", false
			}
			b := p[0]
			p = p[1:]
			n = n<<7 | uint64(b&127)
			if n > uint64(math.MaxUint32)+80 {
				return "", false
			}
			if b&128 == 0 {
				break
			}
		}
		if len(arcs) == 0 {
			switch {
			case n < 40:
				arcs = append(arcs, 0, n)
			case n < 80:
				arcs = append(arcs, 1, n-40)
			default:
				arcs = append(arcs, 2, n-80)
			}
		} else {
			if n > math.MaxUint32 {
				return "", false
			}
			arcs = append(arcs, n)
		}
		if len(arcs) > 128 {
			return "", false
		}
	}
	text := make([]string, len(arcs))
	for i, n := range arcs {
		text[i] = strconv.FormatUint(n, 10)
	}
	return strings.Join(text, "."), true
}

// OIDText requires a canonical dotted SNMP object identifier, without a prefix dot.
func OIDText(text string) bool {
	parts := strings.Split(text, ".")
	if len(parts) < 2 || len(parts) > 128 {
		return false
	}
	var first uint64
	for i, p := range parts {
		if p == "" || len(p) > 1 && p[0] == '0' {
			return false
		}
		n, e := strconv.ParseUint(p, 10, 32)
		if e != nil || strconv.FormatUint(n, 10) != p {
			return false
		}
		if i == 0 {
			first = n
			if n > 2 {
				return false
			}
		}
		if i == 1 && first < 2 && n > 39 {
			return false
		}
	}
	return true
}
