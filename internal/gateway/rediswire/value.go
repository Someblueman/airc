// Package rediswire implements the gateway's bounded RESP2 framing profile.
package rediswire

import "errors"

const (
	Simple      byte = '+'
	Error       byte = '-'
	Integer     byte = ':'
	Bulk        byte = '$'
	Array       byte = '*'
	MaxBulk          = 32768
	MaxLine          = 4096
	MaxElements      = 128
	MaxDepth         = 4
	MaxFrame         = 65536
)

var ErrFrame = errors.New("invalid Redis frame")

// Value preserves RESP type and null/empty distinctions. Only the field for
// Type may be populated; Null is available only for bulk strings and arrays.
type Value struct {
	Type    byte
	Bytes   []byte
	Integer int64
	Array   []Value
	Null    bool
}
