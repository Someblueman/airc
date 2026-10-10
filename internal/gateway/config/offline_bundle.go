package config

import (
	"errors"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func validateOfflineBundle(l Listener) error {
	if l.Protocol != "offline_bundle" {
		return nil
	}
	if l.Address != "" || l.TLSCertFile != "" || l.TLSKeyFile != "" || len(l.AllowedOrigins) != 0 || len(l.Operations) != 1 || l.Operations[0] != core.Send {
		return errors.New("offline bundle requires only explicit send without address, TLS or origins")
	}
	if l.Enabled && !UnixAvailable() {
		return errors.New("offline bundle file import is unavailable on this platform")
	}
	return nil
}
