// Package syslogwire implements bounded RFC 5425 framing and RFC 5424 syntax.
// It performs no sender authentication, freshness verification or dispatch.
package syslogwire

import (
	"bufio"
	"errors"
	"io"
)

const MaxRecord = 32768

var ErrRecord = errors.New("invalid or oversized syslog record")

// Read consumes exactly one octet-counted record. The owner supplies absolute
// socket deadlines and must close after errors; there is no stream resync.
func Read(r *bufio.Reader) (Record, error) {
	n := 0
	for digits := 0; ; digits++ {
		b, err := r.ReadByte()
		if err != nil {
			if digits != 0 && err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return Record{}, err
		}
		if b == ' ' {
			if digits == 0 {
				return Record{}, ErrRecord
			}
			break
		}
		if b < '0' || b > '9' || digits == 0 && b == '0' || digits >= 5 {
			return Record{}, ErrRecord
		}
		n = n*10 + int(b-'0')
		if n > MaxRecord {
			return Record{}, ErrRecord
		}
	}
	raw := make([]byte, n)
	if _, err := io.ReadFull(r, raw); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return Record{}, err
	}
	return Parse(raw)
}
