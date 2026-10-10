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

// ExchangeProtocolHeader advertises the server's sole supported version before
// reading exactly one peer header. On any error the caller must close the
// connection; it must not continue with frames. Callers own deadlines and TLS.
// A pure TLS server uses SASLProtocol after TLS, then AMQPProtocol only after
// successful SASL. This function alone confers no authenticated state.
func ExchangeProtocolHeader(r io.Reader, w io.Writer, protocol byte) error {
	if protocol != AMQPProtocol && protocol != SASLProtocol {
		return ErrHeader
	}
	expected := [8]byte{'A', 'M', 'Q', 'P', protocol, 1, 0, 0}
	n, err := w.Write(expected[:])
	if err != nil {
		return err
	}
	if n != len(expected) {
		return io.ErrShortWrite
	}
	var peer [8]byte
	if _, err := io.ReadFull(r, peer[:]); err != nil {
		return errors.Join(ErrHeader, err)
	}
	if peer != expected {
		return ErrHeader
	}
	return nil
}
