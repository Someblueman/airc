package config

import (
	"errors"
	"net/netip"
	"strings"
)

// ICMPv4 selects the local interface used for Echo request intake.
type ICMPv4 struct {
	Interface string `json:"interface"`
}

// ICMPv4Available reports whether an implementation is present at runtime.
// It does not imply that the profile has been qualified on the host.
func ICMPv4Available() bool { return RawIPAvailable() }

// ParseICMPv4Endpoint parses canonical IPv4:channel endpoints. The channel is
// an Echo identifier, not a UDP port.
func ParseICMPv4Endpoint(endpoint string) (netip.Addr, uint16, error) {
	addr, channel, err := ParseRawIPEndpoint(endpoint)
	if err != nil {
		return netip.Addr{}, 0, errors.New("invalid ICMPv4 endpoint")
	}
	return addr, channel, nil
}

func (options *ICMPv4) Validate(endpoint string) error {
	if options == nil || options.Interface == "" || len(options.Interface) > 64 || strings.TrimSpace(options.Interface) != options.Interface || strings.ContainsAny(options.Interface, "/\\\x00\r\n\t ") {
		return errors.New("invalid ICMPv4 interface")
	}
	_, _, err := ParseICMPv4Endpoint(endpoint)
	return err
}

func (c Config) validateICMPv4() error {
	listeners := make(map[string]Listener)
	endpoints := make(map[string]bool)
	for _, listener := range c.Listeners {
		if listener.Protocol != "icmp_v4" {
			if listener.ICMPv4 != nil {
				return errors.New("ICMPv4 options require an icmp_v4 listener")
			}
			continue
		}
		if err := listener.ICMPv4.Validate(listener.Address); err != nil {
			return err
		}
		if listener.TLSCertFile != "" || listener.TLSKeyFile != "" || len(listener.AllowedOrigins) != 0 || listener.AnonymousPrincipal != "" {
			return errors.New("icmp_v4 cannot use TLS, origins or anonymous access")
		}
		key := listener.ICMPv4.Interface + "\x00" + listener.Address
		if listener.Enabled && endpoints[key] {
			return errors.New("duplicate enabled icmp_v4 interface and endpoint")
		}
		if listener.Enabled {
			endpoints[key] = true
		}
		listeners[listener.ID] = listener
	}

	counts := make(map[string]int)
	for _, route := range c.ReturnRoutes {
		listener, exists := listeners[route.Listener]
		if route.Protocol != "icmp_v4" && !exists {
			continue
		}
		if !exists || route.Protocol != "icmp_v4" || listener.Protocol != "icmp_v4" || !id(route.RecipientKeyID) || !absolute(route.RecipientPublicKeyFile) {
			return errors.New("invalid protected icmp_v4 return route")
		}
		routeAddress, routeChannel, err := ParseICMPv4Endpoint(route.Destination)
		if err != nil {
			return errors.New("icmp_v4 return route requires a canonical IPv4 unicast endpoint")
		}
		sourceAddress, sourceChannel, _ := ParseICMPv4Endpoint(listener.Address)
		if sourceAddress == routeAddress && sourceChannel == routeChannel {
			return errors.New("icmp_v4 return route cannot target the listener endpoint")
		}
		counts[route.Listener+"\x00"+route.Principal]++
	}

	for _, listener := range listeners {
		if !listener.Enabled {
			continue
		}
		if !ICMPv4Available() {
			return errors.New("icmp_v4 listener requires Linux")
		}
		if c.ResponseSigningKey == nil || len(c.RecipientKeys) == 0 || len(c.Principals) == 0 {
			return errors.New("icmp_v4 requires ingress recipient keys, response signing key and principals")
		}
		for _, principal := range c.Principals {
			if len(principal.SigningKeys) == 0 || counts[listener.ID+"\x00"+principal.ID] != 1 {
				return errors.New("icmp_v4 requires a signing principal with exactly one protected return route")
			}
		}
	}
	return nil
}
