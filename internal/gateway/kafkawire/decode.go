package kafkawire

import (
	"encoding/binary"
	"unicode/utf8"
)

// decoder reads only within an already bounded frame. Once malformed, reads
// return zero values and done remains false; no unchecked slicing is permitted.
type decoder struct {
	body []byte
	bad  bool
}

func (d *decoder) take(n int) []byte {
	if d.bad || n < 0 || n > len(d.body) {
		d.bad = true
		return nil
	}
	value := d.body[:n]
	d.body = d.body[n:]
	return value
}
func (d *decoder) i16() int16 {
	b := d.take(2)
	if b == nil {
		return 0
	}
	return int16(binary.BigEndian.Uint16(b))
}
func (d *decoder) i32() int32 {
	b := d.take(4)
	if b == nil {
		return 0
	}
	return int32(binary.BigEndian.Uint32(b))
}
func (d *decoder) i64() int64 {
	b := d.take(8)
	if b == nil {
		return 0
	}
	return int64(binary.BigEndian.Uint64(b))
}
func (d *decoder) text(max int, nullable bool) string {
	size := int(d.i16())
	if size == -1 && nullable {
		return ""
	}
	if size < 0 || size > max {
		d.bad = true
		return ""
	}
	b := d.take(size)
	if !utf8.Valid(b) {
		d.bad = true
		return ""
	}
	return string(b)
}
func (d *decoder) done() bool { return !d.bad && len(d.body) == 0 }
func appendText(body []byte, text string) []byte {
	body = binary.BigEndian.AppendUint16(body, uint16(len(text)))
	return append(body, text...)
}
