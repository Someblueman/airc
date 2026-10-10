// Package amqpwire implements bounded AMQP 1.0 wire primitives. Framing alone
// does not validate performatives, authenticate peers or acknowledge intake.
package amqpwire

import (
	"encoding/binary"
	"errors"
	"io"
)

const (
	InitialFrameLimit = 512
	MaxFrameBytes     = 65536
	AMQP              = byte(0)
	SASL              = byte(1)
)

var ErrFrame = errors.New("invalid or unsupported AMQP frame")

type Frame struct {
	Type    byte
	Channel uint16 // Zero for SASL; its reserved header bytes are ignored.
	Body    []byte
}

// ReadFrame consumes exactly one frame, checking the negotiated inbound limit
// before allocation. Extended headers are ignored as specified for AMQP/SASL.
// The caller owns Body and supplies deadlines and connection-state validation.
func ReadFrame(r io.Reader, limit uint32) (Frame, error) {
	if !validLimit(limit) {
		return Frame{}, ErrFrame
	}
	var header [8]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return Frame{}, err
	}
	size := binary.BigEndian.Uint32(header[:4])
	offset := uint32(header[4]) * 4
	kind := header[5]
	if size < 8 || size > limit || offset < 8 || offset > size || kind > SASL ||
		(kind == SASL && (size > InitialFrameLimit || size == offset)) {
		return Frame{}, ErrFrame
	}
	data := make([]byte, int(size)-8)
	if _, err := io.ReadFull(r, data); err != nil {
		clear(data)
		return Frame{}, err
	}
	clear(data[:offset-8])
	channel := binary.BigEndian.Uint16(header[6:])
	if kind == SASL {
		channel = 0
	}
	return Frame{Type: kind, Channel: channel, Body: data[offset-8:]}, nil
}

// WriteFrame uses the peer's outbound limit and emits no extended header.
// SASL always uses its fixed 512-byte ceiling and zero reserved header bytes.
func WriteFrame(w io.Writer, frame Frame, limit uint32) error {
	if !validLimit(limit) || frame.Type > SASL {
		return ErrFrame
	}
	if frame.Type == SASL {
		limit = InitialFrameLimit
		if frame.Channel != 0 || len(frame.Body) == 0 {
			return ErrFrame
		}
	}
	if len(frame.Body) > int(limit)-8 {
		return ErrFrame
	}
	var header [8]byte
	binary.BigEndian.PutUint32(header[:4], uint32(len(frame.Body))+8)
	header[4], header[5] = 2, frame.Type
	binary.BigEndian.PutUint16(header[6:], frame.Channel)
	for _, part := range [][]byte{header[:], frame.Body} {
		if len(part) == 0 {
			continue
		}
		n, err := w.Write(part)
		if err != nil {
			return err
		}
		if n != len(part) {
			return io.ErrShortWrite
		}
	}
	return nil
}

func validLimit(limit uint32) bool {
	return limit >= InitialFrameLimit && limit <= MaxFrameBytes
}
