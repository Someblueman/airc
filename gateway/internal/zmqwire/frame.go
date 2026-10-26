// Package zmqwire implements the bounded ZMTP 3.1 REQ/REP wire profile with
// the NULL mechanism. NULL grants no sender authority: a request names its
// author, and AIRC checks it.
package zmqwire

import (
	"encoding/binary"
	"errors"
	"io"
)

const MaxData = 65536
const MaxCommand = 4096

var ErrFrame = errors.New("invalid or oversized ZeroMQ frame")

type Frame struct {
	More, Command bool
	Body          []byte
}

// ReadFrame validates flags and the declared size before allocating. It returns
// no partial body on failure and consumes exactly one frame on success.
func ReadFrame(r io.Reader) (Frame, error) {
	if r == nil {
		return Frame{}, ErrFrame
	}
	var header [9]byte
	if _, err := io.ReadFull(r, header[:1]); err != nil {
		return Frame{}, err
	}
	flags := header[0]
	if flags > 7 || flags&5 == 5 {
		return Frame{}, ErrFrame
	}
	sizeBytes := 1
	if flags&2 != 0 {
		sizeBytes = 8
	}
	if _, err := io.ReadFull(r, header[1:1+sizeBytes]); err != nil {
		return Frame{}, err
	}
	size := uint64(header[1])
	if sizeBytes == 8 {
		size = binary.BigEndian.Uint64(header[1:])
	}
	limit := uint64(MaxData)
	if flags&4 != 0 {
		limit = MaxCommand
	}
	if size > limit {
		return Frame{}, ErrFrame
	}
	f := Frame{More: flags&1 != 0, Command: flags&4 != 0, Body: make([]byte, int(size))}
	if _, err := io.ReadFull(r, f.Body); err != nil {
		return Frame{}, err
	}
	if f.Command && !validCommand(f.Body) {
		return Frame{}, ErrFrame
	}
	return f, nil
}

func EncodeFrame(f Frame) ([]byte, error) {
	limit := MaxData
	flags := byte(0)
	if f.More {
		flags |= 1
	}
	if f.Command {
		limit = MaxCommand
		flags |= 4
		if f.More || !validCommand(f.Body) {
			return nil, ErrFrame
		}
	}
	if len(f.Body) > limit {
		return nil, ErrFrame
	}
	var wire []byte
	if len(f.Body) < 256 {
		wire = []byte{flags, byte(len(f.Body))}
	} else {
		wire = binary.BigEndian.AppendUint64([]byte{flags | 2}, uint64(len(f.Body)))
	}
	return append(wire, f.Body...), nil
}

func validCommand(body []byte) bool {
	if len(body) < 2 || body[0] == 0 || int(body[0])+1 > len(body) {
		return false
	}
	for _, c := range body[1 : 1+int(body[0])] {
		if c < 'A' || c > 'Z' {
			if c < 'a' || c > 'z' {
				return false
			}
		}
	}
	return true
}

// Traffic is one unit read between messages: a whole REQ request, or a
// ZMTP 3.1 PING heartbeat command with its context.
type Traffic struct {
	Body    []byte
	Ping    bool
	Context []byte
}

// ReadTraffic reads a PING command (heartbeats arrive only between messages,
// never inside one) or one default REQ envelope: one empty delimiter and one
// final application frame. Correlation envelopes, multipart bodies and other
// commands are refused; no partial message escapes on refusal.
func ReadTraffic(r io.Reader) (Traffic, error) {
	delimiter, err := ReadFrame(r)
	if err != nil {
		return Traffic{}, err
	}
	if delimiter.Command {
		if context, ok := pingContext(delimiter.Body); ok {
			return Traffic{Ping: true, Context: context}, nil
		}
		return Traffic{}, ErrFrame
	}
	if !delimiter.More || len(delimiter.Body) != 0 {
		return Traffic{}, ErrFrame
	}
	body, err := ReadFrame(r)
	if err != nil {
		return Traffic{}, err
	}
	if body.Command || body.More || len(body.Body) == 0 {
		return Traffic{}, ErrFrame
	}
	return Traffic{Body: body.Body}, nil
}

// pingContext parses a PING command body: name, a two-octet TTL and up to 16
// octets of context.
func pingContext(command []byte) ([]byte, bool) {
	const name = "\x04PING"
	if len(command) < len(name)+2 || len(command) > len(name)+2+16 || string(command[:len(name)]) != name {
		return nil, false
	}
	return command[len(name)+2:], true
}

// Pong is the complete PONG command answering a PING with this context.
func Pong(context []byte) ([]byte, error) {
	if len(context) > 16 {
		return nil, ErrFrame
	}
	return EncodeFrame(Frame{Command: true, Body: append([]byte("\x04PONG"), context...)})
}

// Reply emits a complete default REP envelope; caller owns writes/deadlines.
func Reply(body []byte) ([]byte, error) {
	if len(body) == 0 {
		return nil, ErrFrame
	}
	wire, err := EncodeFrame(Frame{Body: body})
	if err != nil {
		return nil, err
	}
	return append([]byte{1, 0}, wire...), nil
}
