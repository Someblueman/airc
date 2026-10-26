package zmqwire

import (
	"bytes"
	"io"
)

// Greeting announces ZMTP 3.1 and NULL, without downgrade negotiation. The
// owner sends this complete greeting under one handshake deadline.
func Greeting() [64]byte {
	var b [64]byte
	b[0], b[9], b[10], b[11] = 255, 127, 3, 1
	copy(b[12:32], "NULL")
	return b
}

func ReadGreeting(r io.Reader) error {
	if r == nil {
		return ErrFrame
	}
	var b [64]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return err
	}
	// RFC 37 forbids interpreting padding and requires accepting newer peers
	// using our own framing. No older protocol or different mechanism fallback.
	if b[0] != 255 || b[9] != 127 || b[10] < 3 || b[10] == 3 && b[11] < 1 || !bytes.Equal(b[12:32], append([]byte("NULL"), make([]byte, 16)...)) || b[32] != 0 {
		return ErrFrame
	}
	for _, c := range b[33:] {
		if c != 0 {
			return ErrFrame
		}
	}
	return nil
}
