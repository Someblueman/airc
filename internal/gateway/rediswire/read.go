package rediswire

import (
	"bufio"
	"io"
	"strconv"
)

type decoder struct {
	r         *bufio.Reader
	remaining int
}

// Read consumes exactly one frame. Any error is terminal for the connection;
// the caller supplies deadlines and must not try to resynchronize this reader.
func Read(r *bufio.Reader) (Value, error) {
	if r == nil {
		return Value{}, ErrFrame
	}
	d := decoder{r: r, remaining: MaxFrame}
	return d.value(0)
}

func (d *decoder) byte() (byte, error) {
	if d.remaining == 0 {
		return 0, ErrFrame
	}
	b, err := d.r.ReadByte()
	if err == nil {
		d.remaining--
	}
	return b, err
}

func (d *decoder) line() ([]byte, error) {
	b := make([]byte, 0, 32)
	for {
		c, err := d.byte()
		if err != nil {
			return nil, err
		}
		if c == '\r' {
			n, err := d.byte()
			if err != nil {
				return nil, err
			}
			if n != '\n' {
				return nil, ErrFrame
			}
			return b, nil
		}
		if c == '\n' || len(b) == MaxLine {
			return nil, ErrFrame
		}
		b = append(b, c)
	}
}

func length(b []byte, max int) (int, bool, error) {
	if string(b) == "-1" {
		return 0, true, nil
	}
	if len(b) == 0 {
		return 0, false, ErrFrame
	}
	for _, c := range b {
		if c < '0' || c > '9' {
			return 0, false, ErrFrame
		}
	}
	n, err := strconv.ParseUint(string(b), 10, 64)
	if err != nil || n > uint64(max) {
		return 0, false, ErrFrame
	}
	return int(n), false, nil
}

func (d *decoder) value(depth int) (Value, error) {
	t, err := d.byte()
	if err != nil {
		return Value{}, err
	}
	if t != Simple && t != Error && t != Integer && t != Bulk && t != Array {
		return Value{}, ErrFrame
	}
	b, err := d.line()
	if err != nil {
		return Value{}, err
	}
	v := Value{Type: t}
	switch t {
	case Simple, Error:
		v.Bytes = b
	case Integer:
		v.Integer, err = strconv.ParseInt(string(b), 10, 64)
		if err != nil {
			return Value{}, ErrFrame
		}
	case Bulk:
		n, null, err := length(b, MaxBulk)
		if err != nil {
			return Value{}, err
		}
		v.Null = null
		if null {
			return v, nil
		}
		if n+2 > d.remaining {
			return Value{}, ErrFrame
		}
		body := make([]byte, n+2)
		if _, err := io.ReadFull(d.r, body); err != nil {
			return Value{}, err
		}
		d.remaining -= n + 2
		if body[n] != '\r' || body[n+1] != '\n' {
			return Value{}, ErrFrame
		}
		v.Bytes = body[:n:n]
	case Array:
		if depth >= MaxDepth {
			return Value{}, ErrFrame
		}
		n, null, err := length(b, MaxElements)
		if err != nil {
			return Value{}, err
		}
		v.Null = null
		if null {
			return v, nil
		}
		// Every child requires at least a type byte and CRLF. Check the
		// minimum encoded budget before allocating the aggregate's slots.
		if n > d.remaining/3 {
			return Value{}, ErrFrame
		}
		v.Array = make([]Value, n)
		for i := range v.Array {
			v.Array[i], err = d.value(depth + 1)
			if err != nil {
				return Value{}, err
			}
		}
	}
	return v, nil
}
