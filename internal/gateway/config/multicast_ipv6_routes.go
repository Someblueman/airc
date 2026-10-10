package config

import (
	"errors"
	"net"
	"net/netip"
	"runtime"
	"strconv"
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
	counts := make(map[string]int)
	for _, route := range c.ReturnRoutes {
		if route.Protocol != "ipv6_multicast" {
			continue
		}
		if _, ok := listeners[route.Listener]; !ok || !id(route.RecipientKeyID) || !absolute(route.RecipientPublicKeyFile) {
			return errors.New("invalid IPv6 multicast protected return route")
		}
		host, port, err := net.SplitHostPort(route.Destination)
		ip, ipErr := netip.ParseAddr(host)
		n, portErr := strconv.Atoi(port)
		listener := listeners[route.Listener]
		if err != nil || ipErr != nil || portErr != nil || !ip.Is6() || ip.Is4In6() || ip.IsUnspecified() || ip.IsMulticast() || n < 1 || n > 65535 || strconv.Itoa(n) != port || net.JoinHostPort(ip.String(), port) != route.Destination {
			return errors.New("IPv6 multicast return route requires a canonical IPv6 unicast address")
		}
		if ip.IsLinkLocalUnicast() {
			if ip.Zone() != listener.IPv6Multicast.Interface {
				return errors.New("IPv6 link-local return route requires the listener interface zone")
			}
		} else if ip.Zone() != "" {
			return errors.New("IPv6 non-link-local return route cannot specify a zone")
		}
		counts[route.Listener+"\x00"+route.Principal]++
	}
	for _, listener := range listeners {
		if !listener.Enabled {
			continue
		}
		if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
			return errors.New("IPv6 multicast requires Darwin or Linux socket ownership")
		}
		if c.ResponseSigningKey == nil || len(c.RecipientKeys) == 0 || len(c.Principals) == 0 {
			return errors.New("IPv6 multicast requires ingress recipient keys, response signing key and principals")
		}
		for _, principal := range c.Principals {
			if len(principal.SigningKeys) == 0 || counts[listener.ID+"\x00"+principal.ID] != 1 {
				return errors.New("IPv6 multicast requires a signing principal with exactly one protected return route")
			}
		}
	}
	return nil
}
