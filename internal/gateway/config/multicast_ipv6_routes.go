package config

import (
	"errors"
	"runtime"
)

func (c Config) validateIPv6Multicast() error {
	listeners := make(map[string]Listener)
	for _, listener := range c.Listeners {
		if listener.Protocol != "ipv6_multicast" {
			if listener.IPv6Multicast != nil {
				return errors.New("IPv6 multicast options require an IPv6 multicast listener")
			}
			continue
		}
		if err := listener.IPv6Multicast.Validate(listener.Address); err != nil {
			return err
		}
		if listener.TLSCertFile != "" || listener.TLSKeyFile != "" || len(listener.AllowedOrigins) != 0 {
			return errors.New("IPv6 multicast cannot use TLS or origins")
		}
		listeners[listener.ID] = listener
	}
	for _, listener := range listeners {
		if !listener.Enabled {
			continue
		}
		if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
			return errors.New("IPv6 multicast requires Darwin or Linux socket ownership")
		}
	}
	return nil
}
