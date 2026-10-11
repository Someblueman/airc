package config

import (
	"errors"

	"github.com/Someblueman/airc/internal/gateway/bpv7"
	"github.com/Someblueman/airc/internal/gateway/core"
)

type BundleProtocol struct {
	Source      string      `json:"source"`
	Destination string      `json:"destination"`
	Target      core.Target `json:"target"`
}

func (c Config) validateBundleProtocol(l Listener) error {
	m := l.BundleProtocol
	if m == nil || !bpv7.Endpoint(m.Source, true) || !bpv7.Endpoint(m.Destination, false) || l.Address != "" || l.TLSCertFile != "" || l.TLSKeyFile != "" || len(l.AllowedOrigins) != 0 || len(l.Operations) != 1 || l.Operations[0] != core.Send {
		return errors.New("BPv7 requires bounded source/destination mapping and only explicit send without address, TLS or origins")
	}
	if l.Enabled && !Available("bpv7") {
		return errors.New("BPv7 file import is unavailable on this platform")
	}
	// The payload names its author; the mapping fixes the one target it
	// may send to.
	target := m.Target
	if err := target.Normalize(); err != nil || target != m.Target {
		return errors.New("BPv7 mapping requires a normalized target")
	}
	return nil
}
