package config

import (
	"errors"
	"runtime"
)

func (c Config) validateIPv4Broadcast() error {
	listeners := make(map[string]Listener)
	for _, listener := range c.Listeners {
		if listener.Protocol != "ipv4_broadcast" {
			if listener.IPv4Broadcast != nil {
				return errors.New("IPv4 broadcast options require an IPv4 broadcast listener")
			}
			continue
		}
		if err := listener.IPv4Broadcast.Validate(listener.Address); err != nil {
			return err
		}
		if listener.TLSCertFile != "" || listener.TLSKeyFile != "" || len(listener.AllowedOrigins) != 0 {
			return errors.New("IPv4 broadcast cannot use TLS or origins")
		}
		listeners[listener.ID] = listener
	}
	for _, listener := range listeners {
		if !listener.Enabled {
			continue
		}
		if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
			return errors.New("IPv4 broadcast requires Darwin or Linux socket ownership")
		}
	}
	return nil
}
