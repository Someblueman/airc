package config

import "errors"

func (c Config) validateUDPLite() error {
	listeners := make(map[string]Listener)
	for _, listener := range c.Listeners {
		if listener.Protocol != "udp_lite" {
			if listener.UDPLite != nil {
				return errors.New("UDP-Lite options require an UDP-Lite listener")
			}
			continue
		}
		if err := listener.UDPLite.Validate(listener.Address); err != nil {
			return err
		}
		if listener.TLSCertFile != "" || listener.TLSKeyFile != "" || len(listener.AllowedOrigins) != 0 {
			return errors.New("UDP-Lite cannot use TLS or origins")
		}
		listeners[listener.ID] = listener
	}
	for _, listener := range listeners {
		if !listener.Enabled {
			continue
		}
		if !Available("udp_lite") {
			return errors.New("UDP-Lite service requires Linux socket ownership")
		}
	}
	return nil
}
