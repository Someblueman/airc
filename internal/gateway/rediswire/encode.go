package rediswire

import (
	"bytes"
	"strconv"
)

// Encode checks shape, depth and total size before allocating the wire frame.
func Encode(v Value) ([]byte, error) {
	n, err := size(v, 0)
	if err != nil {
		return nil, err
	}
	b := make([]byte, 0, n)
	return appendValue(b, v), nil
}

func size(v Value, depth int) (int, error) {
	if v.Type != Integer && v.Integer != 0 || v.Type != Array && v.Array != nil || v.Type != Simple && v.Type != Error && v.Type != Bulk && v.Bytes != nil || v.Null && v.Type != Bulk && v.Type != Array {
		return 0, ErrFrame
	}
	switch v.Type {
	case Simple, Error:
		if len(v.Bytes) > MaxLine || bytes.ContainsAny(v.Bytes, "\r\n") {
			return 0, ErrFrame
		}
		return len(v.Bytes) + 3, nil
	case Integer:
		return len(strconv.FormatInt(v.Integer, 10)) + 3, nil
	case Bulk:
		if v.Null {
			if v.Bytes != nil {
				return 0, ErrFrame
			}
			return 5, nil
		}
		if len(v.Bytes) > MaxBulk {
			return 0, ErrFrame
		}
		return len(strconv.Itoa(len(v.Bytes))) + len(v.Bytes) + 5, nil
	case Array:
		if depth >= MaxDepth || len(v.Array) > MaxElements {
			return 0, ErrFrame
		}
		if v.Null {
			if v.Array != nil {
				return 0, ErrFrame
			}
			return 5, nil
		}
		n := len(strconv.Itoa(len(v.Array))) + 3
		for _, child := range v.Array {
			m, err := size(child, depth+1)
			if err != nil || m > MaxFrame-n {
				return 0, ErrFrame
			}
			n += m
		}
		return n, nil
	default:
		return 0, ErrFrame
	}
}

func appendValue(b []byte, v Value) []byte {
	b = append(b, v.Type)
	switch v.Type {
	case Simple, Error:
		b = append(b, v.Bytes...)
		return append(b, '\r', '\n')
	case Integer:
		b = strconv.AppendInt(b, v.Integer, 10)
		return append(b, '\r', '\n')
	case Bulk, Array:
		if v.Null {
			return append(b, '-', '1', '\r', '\n')
		}
		if v.Type == Bulk {
			b = strconv.AppendInt(b, int64(len(v.Bytes)), 10)
			b = append(b, '\r', '\n')
			b = append(b, v.Bytes...)
			return append(b, '\r', '\n')
		}
		b = strconv.AppendInt(b, int64(len(v.Array)), 10)
		b = append(b, '\r', '\n')
		for _, child := range v.Array {
			b = appendValue(b, child)
		}
	}
	return b
}
