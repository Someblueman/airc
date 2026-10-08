// Package nngwire implements a bounded direct REQ0/REP0 SP/TCP profile.
// The wire correlation identifier grants no sender authority. The owner must
// restrict the listener to loopback and authenticate encrypted signed envelopes.
package nngwire

import (
	"encoding/binary"
	"errors"
	"io"
)

const MaxData = 65536

var ErrFrame = errors.New("invalid or oversized NNG frame")

// Greeting advertises REP0. Both peers send their complete headers immediately;
// the owner must impose a handshake deadline and await the peer header.
func Greeting() [8]byte { return [8]byte{0, 'S', 'P', 0, 0, 0x31, 0, 0} }

func ReadGreeting(r io.Reader) error {
	if r == nil {
		return ErrFrame
	}
	var header [8]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return err
	}
	if header != [8]byte{0, 'S', 'P', 0, 0, 0x30, 0, 0} {
		return ErrFrame
	}
	return nil
}

// Request contains one whole application message and an opaque wire handle.
// The signed AIRC request ID remains inside Body and is authenticated separately.
type Request struct {
	Correlation uint32
	Body        []byte
}

func ReadRequest(r io.Reader) (Request, error) {
	if r == nil {
		return Request{}, ErrFrame
	}
	var header [12]byte
	if _, err := io.ReadFull(r, header[:8]); err != nil {
		return Request{}, err
	}
	size := binary.BigEndian.Uint64(header[:8])
	// SP length includes the backtrace. This direct profile allows exactly one
	// terminal request identifier, with no forwarding hops and a nonempty body.
	if size < 5 || size > MaxData+4 {
		return Request{}, ErrFrame
	}
	if _, err := io.ReadFull(r, header[8:]); err != nil {
		return Request{}, err
	}
	correlation := binary.BigEndian.Uint32(header[8:])
	if correlation&0x80000000 == 0 {
		return Request{}, ErrFrame
	}
	body := make([]byte, int(size-4))
	if _, err := io.ReadFull(r, body); err != nil {
		return Request{}, err
	}
	return Request{Correlation: correlation, Body: body}, nil
}

// Reply echoes the terminal request identifier without deriving AIRC authority
// or durable identity from it. The returned complete frame owns its bytes.
func Reply(correlation uint32, body []byte) ([]byte, error) {
	if correlation&0x80000000 == 0 || len(body) == 0 || len(body) > MaxData {
		return nil, ErrFrame
	}
	wire := binary.BigEndian.AppendUint64(nil, uint64(len(body)+4))
	wire = binary.BigEndian.AppendUint32(wire, correlation)
	return append(wire, body...), nil
}
