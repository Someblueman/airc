package config

import (
	"errors"
	"runtime"
)

func (c Config) validateIPv4Broadcast() error {
	return c.validatePacketGroup("ipv4_broadcast", "IPv4 broadcast", func(l Listener) (packetGroupOptions, bool) { return l.IPv4Broadcast, l.IPv4Broadcast != nil })
}

func (c Config) validateIPv4Multicast() error {
	return c.validatePacketGroup("ipv4_multicast", "IPv4 multicast", func(l Listener) (packetGroupOptions, bool) { return l.IPv4Multicast, l.IPv4Multicast != nil })
}

func (c Config) validateIPv6Multicast() error {
	return c.validatePacketGroup("ipv6_multicast", "IPv6 multicast", func(l Listener) (packetGroupOptions, bool) { return l.IPv6Multicast, l.IPv6Multicast != nil })
}

type packetGroupOptions interface{ Validate(address string) error }

// validatePacketGroup checks the listeners of one broadcast or multicast
// protocol: its options appear only on that protocol and are valid, it has no
// TLS or origins, and an enabled one needs Darwin or Linux socket ownership.
func (c Config) validatePacketGroup(protocol, name string, options func(Listener) (packetGroupOptions, bool)) error {
	enabled := false
	for _, listener := range c.Listeners {
		o, set := options(listener)
		if listener.Protocol != protocol {
			if set {
				return errors.New(name + " options require an " + name + " listener")
			}
			continue
		}
		if err := o.Validate(listener.Address); err != nil {
			return err
		}
		if listener.TLSCertFile != "" || listener.TLSKeyFile != "" || len(listener.AllowedOrigins) != 0 {
			return errors.New(name + " cannot use TLS or origins")
		}
		enabled = enabled || listener.Enabled
	}
	if enabled && runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return errors.New(name + " requires Darwin or Linux socket ownership")
	}
	return nil
}
