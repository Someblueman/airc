package config

import (
	"errors"
	"path/filepath"
)

// FIFO fixes the sole authenticated authority for one private FIFO pair.
type FIFO struct {
	Principal string `json:"principal"`
}

func (c Config) validateFIFO(l Listener) error {
	if l.Protocol != "fifo" {
		if l.FIFO != nil {
			return errors.New("FIFO options require a fifo listener")
		}
		return nil
	}
	if !absolute(l.Address) || filepath.Clean(l.Address) != l.Address || l.TLSCertFile != "" || l.TLSKeyFile != "" || len(l.AllowedOrigins) > 0 {
		return errors.New("FIFO requires a normalized absolute private root without TLS or origins")
	}
	if len(l.Operations) == 0 {
		return errors.New("FIFO requires explicit operations")
	}
	if c.LocalAuthorMode(l) {
		// Plain author requests: the private FIFO pair is the access gate.
		return nil
	}
	if !id(l.FIFO.Principal) {
		return errors.New("FIFO requires an explicit sender principal")
	}
	found := false
	for _, p := range c.Principals {
		if p.ID == l.FIFO.Principal {
			if p.Revoked || len(p.SigningKeys) == 0 {
				return errors.New("FIFO sender must be active with a signing key")
			}
			found = true
		} else if l.Enabled && !p.Revoked {
			return errors.New("FIFO return channel requires exactly one active sender principal")
		}
	}
	if !found {
		return errors.New("FIFO sender is unknown")
	}
	return nil
}
