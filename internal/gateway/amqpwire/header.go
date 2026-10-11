package amqpwire

import (
	"errors"
	"io"
)

const (
	AMQPProtocol byte = 0
	SASLProtocol byte = 3
)

var ErrHeader = errors.New("unsupported AMQP protocol header")

// ReadProtocolHeader reads exactly one peer protocol header and returns its
// protocol ID (AMQPProtocol or SASLProtocol), so a server can answer a client
// that skips SASL. Any other header returns ErrHeader; the caller then writes
// its own supported header and closes, as AMQP 1.0 section 2.2 requires.
func ReadProtocolHeader(r io.Reader) (byte, error) {
	var peer [8]byte
	if _, err := io.ReadFull(r, peer[:]); err != nil {
		return 0, errors.Join(ErrHeader, err)
	}
	if string(peer[:4]) != "AMQP" || peer[5] != 1 || peer[6] != 0 || peer[7] != 0 || peer[4] != AMQPProtocol && peer[4] != SASLProtocol {
		return 0, ErrHeader
	}
	return peer[4], nil
}

// WriteProtocolHeader writes the header for protocol.
func WriteProtocolHeader(w io.Writer, protocol byte) error {
	if protocol != AMQPProtocol && protocol != SASLProtocol {
		return ErrHeader
	}
	header := [8]byte{'A', 'M', 'Q', 'P', protocol, 1, 0, 0}
	n, err := w.Write(header[:])
	if err != nil {
		return err
	}
	if n != len(header) {
		return io.ErrShortWrite
	}
	return nil
}
