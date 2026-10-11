package config

import (
	"errors"
	"path/filepath"
)

func (Config) validateFIFO(l Listener) error {
	if !absolute(l.Address) || filepath.Clean(l.Address) != l.Address || l.TLSCertFile != "" || l.TLSKeyFile != "" || len(l.AllowedOrigins) > 0 {
		return errors.New("FIFO requires a normalized absolute private root without TLS or origins")
	}
	if len(l.Operations) == 0 {
		return errors.New("FIFO requires explicit operations")
	}
	// The private FIFO pair is the access gate; requests name their author.
	return nil
}
