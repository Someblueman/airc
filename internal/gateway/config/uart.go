package config

import (
	"errors"
	"path/filepath"
)

// UART uses one explicitly selected tty, with TLS and fixed 115200 8N1 framing.
func (c Config) validateUART(l Listener) error {
	if l.Protocol != "uart" {
		return nil
	}
	if !absolute(l.Address) || filepath.Clean(l.Address) != l.Address || len(l.Address) > 4096 {
		return errors.New("UART requires a bounded absolute normalized device path")
	}
	if l.TLSCertFile == "" || l.TLSKeyFile == "" {
		return errors.New("UART requires TLS certificates")
	}
	if len(l.Operations) == 0 {
		return errors.New("UART requires explicit operations")
	}
	if l.Enabled {
		for _, b := range c.RedisPubSub {
			if b.Enabled {
				return errors.New("UART cannot run alongside an enabled broker")
			}
		}
		for _, b := range c.RedisStreams {
			if b.Enabled {
				return errors.New("UART cannot run alongside an enabled broker")
			}
		}
		for _, b := range c.IRCBridges {
			if b.Enabled {
				return errors.New("UART cannot run alongside an enabled bridge")
			}
		}
	}
	return nil
}
