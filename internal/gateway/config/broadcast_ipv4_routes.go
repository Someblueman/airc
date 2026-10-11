package config

import (
	"errors"
	"net"
	"net/netip"
	"runtime"
	"strconv"
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
		if listener.AnonymousPrincipal != "" {
			return errors.New("IPv4 broadcast cannot use anonymous access")
		}
		listeners[listener.ID] = listener
	}
	counts := make(map[string]int)
	for _, route := range c.ReturnRoutes {
		listener, exists := listeners[route.Listener]
		if route.Protocol != "ipv4_broadcast" && !exists {
			continue
		}
		if !exists || route.Protocol != "ipv4_broadcast" || listener.Protocol != "ipv4_broadcast" || !id(route.RecipientKeyID) || !absolute(route.RecipientPublicKeyFile) {
			return errors.New("invalid IPv4 broadcast protected return route")
		}
		if err := udpDestination(route.Destination); err != nil {
			return err
		}
		host, port, _ := net.SplitHostPort(route.Destination)
		ip, err := netip.ParseAddr(host)
		n, _ := strconv.Atoi(port)
		if err != nil || !ip.Is4() || strconv.Itoa(n) != port || net.JoinHostPort(ip.String(), port) != route.Destination {
			return errors.New("IPv4 broadcast return route requires a canonical IPv4 unicast address")
		}
		subnet, _ := netip.ParsePrefix(listener.IPv4Broadcast.Subnet)
		if ip == subnet.Addr() || listener.IPv4Broadcast.Validate(route.Destination) == nil {
			return errors.New("IPv4 broadcast return route cannot target the subnet network or broadcast address")
		}
		counts[route.Listener+"\x00"+route.Principal]++
	}
	for _, listener := range listeners {
		if !listener.Enabled {
			continue
		}
		if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
			return errors.New("IPv4 broadcast requires Darwin or Linux socket ownership")
		}
		// Without protected return routes the listener carries plain author
		// requests and replies to their source; no keys are needed.
		if !c.LegacyRoutes(listener.ID) {
			continue
		}
		if c.ResponseSigningKey == nil || len(c.RecipientKeys) == 0 || len(c.Principals) == 0 {
			return errors.New("IPv4 broadcast requires ingress recipient keys, response signing key and principals")
		}
		for _, principal := range c.Principals {
			if len(principal.SigningKeys) == 0 || counts[listener.ID+"\x00"+principal.ID] != 1 {
				return errors.New("IPv4 broadcast requires a signing principal with exactly one protected return route")
			}
		}
	}
	return nil
}
