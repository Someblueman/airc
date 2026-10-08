package config

import (
	"errors"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

// IPv4Multicast pins one local interface; membership never grants sender authority.
type IPv4Multicast struct {
	Interface string `json:"interface"`
}

// Validate checks endpoint spelling and the explicit interface selection.
func (options *IPv4Multicast) Validate(address string) error {
	if options == nil || options.Interface == "" || len(options.Interface) > 64 || strings.TrimSpace(options.Interface) != options.Interface || strings.ContainsAny(options.Interface, "/\\\x00\r\n\t ") {
		return errors.New("invalid IPv4 multicast interface")
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return errors.New("invalid IPv4 multicast endpoint")
	}
	group, err := netip.ParseAddr(host)
	if err != nil || !group.Is4() || !group.IsMulticast() || !netip.MustParsePrefix("239.0.0.0/8").Contains(group) {
		return errors.New("IPv4 multicast requires an administratively scoped group")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port || net.JoinHostPort(group.String(), port) != address {
		return errors.New("invalid IPv4 multicast port or spelling")
	}
	return nil
}

func (c Config) validateIPv4Multicast() error {
	listeners := make(map[string]Listener)
	for _, listener := range c.Listeners {
		if listener.Protocol != "ipv4_multicast" {
			if listener.IPv4Multicast != nil {
				return errors.New("IPv4 multicast options require an IPv4 multicast listener")
			}
			continue
		}
		if err := listener.IPv4Multicast.Validate(listener.Address); err != nil {
			return err
		}
		if listener.TLSCertFile != "" || listener.TLSKeyFile != "" || len(listener.AllowedOrigins) != 0 {
			return errors.New("IPv4 multicast cannot use TLS or origins")
		}
		listeners[listener.ID] = listener
	}
	counts := make(map[string]int)
	for _, route := range c.ReturnRoutes {
		if route.Protocol != "ipv4_multicast" {
			continue
		}
		if _, ok := listeners[route.Listener]; !ok || !id(route.RecipientKeyID) || !absolute(route.RecipientPublicKeyFile) {
			return errors.New("invalid IPv4 multicast protected return route")
		}
		if err := udpDestination(route.Destination); err != nil {
			return err
		}
		host, port, _ := net.SplitHostPort(route.Destination)
		ip, err := netip.ParseAddr(host)
		n, _ := strconv.Atoi(port)
		if err != nil || !ip.Is4() || strconv.Itoa(n) != port || net.JoinHostPort(ip.String(), port) != route.Destination {
			return errors.New("IPv4 multicast return route requires a canonical IPv4 unicast address")
		}
		counts[route.Listener+"\x00"+route.Principal]++
	}
	for _, listener := range listeners {
		if !listener.Enabled {
			continue
		}
		if c.ResponseSigningKey == nil || len(c.RecipientKeys) == 0 || len(c.Principals) == 0 {
			return errors.New("IPv4 multicast requires ingress recipient keys, response signing key and principals")
		}
		for _, principal := range c.Principals {
			if len(principal.SigningKeys) == 0 || counts[listener.ID+"\x00"+principal.ID] != 1 {
				return errors.New("IPv4 multicast requires a signing principal with exactly one protected return route")
			}
		}
	}
	return nil
}
