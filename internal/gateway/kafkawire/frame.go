// Package kafkawire implements bounded non-flexible Kafka framing. Socket
// ownership, deadlines, authentication and command policy belong to callers.
package kafkawire

import (
	"encoding/binary"
	"errors"
	"io"
)

const MaxFrame = 1 << 20
const ClientID = "airc-gateway"

var ErrFrame = errors.New("invalid or oversized Kafka frame")

// Request encodes a request with header version 1 and an owned body. API version
// support and command-specific body validation are the command owner's policy.
func Request(api, version int16, correlation int32, body []byte) ([]byte, error) {
	const header = 2 + 2 + 4 + 2 + len(ClientID)
	if api < 0 || version < 0 || correlation < 0 || len(body) > MaxFrame-header {
		return nil, ErrFrame
	}
	wire := make([]byte, 4+header+len(body))
	binary.BigEndian.PutUint32(wire, uint32(header+len(body)))
	binary.BigEndian.PutUint16(wire[4:], uint16(api))
	binary.BigEndian.PutUint16(wire[6:], uint16(version))
	binary.BigEndian.PutUint32(wire[8:], uint32(correlation))
	binary.BigEndian.PutUint16(wire[12:], uint16(len(ClientID)))
	copy(wire[14:], ClientID)
	copy(wire[4+header:], body)
	return wire, nil
}

// Response reads one header-version-0 response and returns an owned command
// body. It leaves following frames unread. Any error is terminal for the socket;
// callers must close it, never attempt frame resynchronization.
func Response(r io.Reader, correlation int32) ([]byte, error) {
	if r == nil || correlation < 0 {
		return nil, ErrFrame
	}
	var header [8]byte
	if _, err := io.ReadFull(r, header[:4]); err != nil {
		return nil, err
	}
	size := int32(binary.BigEndian.Uint32(header[:4]))
	if size < 4 || size > MaxFrame {
		return nil, ErrFrame
	}
	if _, err := io.ReadFull(r, header[4:]); err != nil {
		return nil, err
	}
	if int32(binary.BigEndian.Uint32(header[4:])) != correlation {
		return nil, ErrFrame
	}
	body := make([]byte, int(size)-4)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}
