package config

import "errors"

// OSCORE selects an existing provisioned context and the separately built native
// worker. Configuration never imports or initializes cryptographic state.
type OSCORE struct {
	Binary      string `json:"binary"`
	ContextFile string `json:"context_file"`
	Principal   string `json:"principal"`
}

func (c Config) validateOSCORE(l Listener) error {
	if l.Protocol != "coap_oscore" {
		if l.OSCORE != nil {
			return errors.New("OSCORE options require a coap_oscore listener")
		}
		return nil
	}
	if l.OSCORE == nil || !absolute(l.OSCORE.Binary) || !absolute(l.OSCORE.ContextFile) || !id(l.OSCORE.Principal) {
		return errors.New("OSCORE listener requires an absolute binary, context file, and fixed principal")
	}
	if l.TLSCertFile != "" || l.TLSKeyFile != "" || len(l.AllowedOrigins) != 0 {
		return errors.New("OSCORE listener cannot use TLS or origins")
	}
	if l.Enabled && !UnixAvailable() {
		return errors.New("native OSCORE listener is unavailable on this platform")
	}
	for _, p := range c.Principals {
		if p.ID == l.OSCORE.Principal {
			if p.Revoked || len(p.SigningKeys) == 0 {
				return errors.New("OSCORE owner must be active with a signing key")
			}
			return nil
		}
	}
	return errors.New("OSCORE listener owner is unknown")
}
