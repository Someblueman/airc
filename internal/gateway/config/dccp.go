package config

import (
	"errors"
	"net"
	"net/netip"
	"runtime"
	"strconv"
)

// DCCPAvailable reports whether a DCCP implementation exists.
func DCCPAvailable() bool { return runtime.GOOS == "linux" }

// ParseDCCPEndpoint parses a canonical numeric IPv4 address and port.
func ParseDCCPEndpoint(endpoint string) (netip.Addr, uint16, error) {
	host, portText, err := net.SplitHostPort(endpoint)
	if err != nil {
		return netip.Addr{}, 0, errors.New("invalid DCCP endpoint")
	}
	addr, addrErr := netip.ParseAddr(host)
	port, portErr := strconv.ParseUint(portText, 10, 16)
	if addrErr != nil || portErr != nil || !addr.Is4() || addr.IsUnspecified() || addr.IsMulticast() || addr == netip.MustParseAddr("255.255.255.255") || port == 0 || strconv.FormatUint(port, 10) != portText || net.JoinHostPort(addr.String(), portText) != endpoint {
		return netip.Addr{}, 0, errors.New("DCCP endpoint requires a canonical IPv4 unicast address and port")
	}
	return addr, uint16(port), nil
}

func (c Config) validateDCCP() error {
	listeners := make(map[string]Listener)
	for _, listener := range c.Listeners {
		if listener.Protocol != "dccp" {
			continue
		}
		if _, _, err := ParseDCCPEndpoint(listener.Address); err != nil {
			return err
		}
		if listener.TLSCertFile != "" || listener.TLSKeyFile != "" || len(listener.AllowedOrigins) != 0 || listener.AnonymousPrincipal != "" {
			return errors.New("DCCP cannot use TLS, origins or anonymous access")
		}
		listeners[listener.ID] = listener
	}

	counts := make(map[string]int)
	for _, route := range c.ReturnRoutes {
		listener, exists := listeners[route.Listener]
		if route.Protocol != "dccp" && !exists {
			continue
		}
		if !exists || route.Protocol != "dccp" || listener.Protocol != "dccp" || !id(route.RecipientKeyID) || !absolute(route.RecipientPublicKeyFile) {
			return errors.New("invalid protected DCCP return route")
		}
		routeAddress, routePort, err := ParseDCCPEndpoint(route.Destination)
		if err != nil {
			return errors.New("DCCP return route requires a canonical IPv4 unicast endpoint")
		}
		listenerAddress, listenerPort, _ := ParseDCCPEndpoint(listener.Address)
		if listenerAddress == routeAddress && listenerPort == routePort {
			return errors.New("DCCP return route cannot target the listener endpoint")
		}
		counts[route.Listener+"\x00"+route.Principal]++
	}

	for _, listener := range listeners {
		if !listener.Enabled {
			continue
		}
		if !DCCPAvailable() {
			return errors.New("DCCP listener requires Linux")
		}
		if c.ResponseSigningKey == nil || len(c.RecipientKeys) == 0 || len(c.Principals) == 0 {
			return errors.New("DCCP requires ingress recipient keys, response signing key and principals")
		}
		for _, principal := range c.Principals {
			if len(principal.SigningKeys) == 0 || counts[listener.ID+"\x00"+principal.ID] != 1 {
				return errors.New("DCCP requires a signing principal with exactly one protected return route")
			}
		}
	}
	return nil
}
