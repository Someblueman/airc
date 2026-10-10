package bluezgatt

import (
	"context"
	"errors"
)

// Peer is one configured BlueZ LE device address. It is transport admission
// metadata, not an AIRC identity.
type Peer struct {
	Address     string
	AddressType string
}

// Handler processes a committed opaque transfer and returns its opaque result.
type Handler func(context.Context, string, [8]byte, []byte) ([]byte, error)

// Server is a prepared GATT application. Run activates transfer methods after
// the containing service has completed its own startup checks.
type Server struct {
	run   func(context.Context) error
	close func() error
}

func (s *Server) Run(ctx context.Context) error {
	if s == nil || s.run == nil {
		return errors.New("BlueZ GATT server is unavailable")
	}
	return s.run(ctx)
}

func (s *Server) Close() error {
	if s == nil || s.close == nil {
		return nil
	}
	return s.close()
}
