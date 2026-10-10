package config

import (
	"errors"

	"github.com/Someblueman/airc/internal/gateway/bpv7"
	"github.com/Someblueman/airc/internal/gateway/core"
)

type BundleProtocol struct {
	Source      string      `json:"source"`
	Destination string      `json:"destination"`
	Principal   string      `json:"principal"`
	Target      core.Target `json:"target"`
}

func (c Config) validateBundleProtocol(l Listener) error {
	if l.Protocol != "bpv7" {
		if l.BundleProtocol != nil {
			return errors.New("bundle_protocol requires bpv7 protocol")
		}
		return nil
	}
	m := l.BundleProtocol
	if m == nil || !bpv7.Endpoint(m.Source, true) || !bpv7.Endpoint(m.Destination, false) || !id(m.Principal) || l.Address != "" || l.TLSCertFile != "" || l.TLSKeyFile != "" || len(l.AllowedOrigins) != 0 || len(l.Operations) != 1 || l.Operations[0] != core.Send {
		return errors.New("BPv7 requires bounded source/destination mapping and only explicit send without address, TLS or origins")
	}
	if l.Enabled && !UnixAvailable() {
		return errors.New("BPv7 file import is unavailable on this platform")
	}
	for _, p := range c.Principals {
		if p.ID != m.Principal || p.Revoked || len(p.SigningKeys) == 0 {
			continue
		}
		canSend := false
		for _, op := range p.Operations {
			canSend = canSend || op == core.Send
		}
		for _, target := range p.Targets {
			if canSend && target == m.Target {
				return nil
			}
		}
	}
	return errors.New("BPv7 mapping requires an active signing principal authorized for the exact target")
}
