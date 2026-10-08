package config

import (
	"errors"
	"net"
	"net/netip"
	"strconv"
)

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
	counts := make(map[string]int)
	for _, route := range c.ReturnRoutes {
		if route.Protocol != "udp_lite" {
			continue
		}
		if _, ok := listeners[route.Listener]; !ok || !id(route.RecipientKeyID) || !absolute(route.RecipientPublicKeyFile) {
			return errors.New("invalid UDP-Lite protected return route")
		}
		listener := listeners[route.Listener]
		if err := listener.UDPLite.Validate(route.Destination); err != nil {
			return err
		}
		host, port, _ := net.SplitHostPort(route.Destination)
		ip, _ := netip.ParseAddr(host)
		n, _ := strconv.Atoi(port)
		listenerHost, _, _ := net.SplitHostPort(listener.Address)
		listenerIP, _ := netip.ParseAddr(listenerHost)
		if n == 0 || ip.IsUnspecified() || ip.Is4() != listenerIP.Is4() {
			return errors.New("UDP-Lite return route requires same-family unicast and a nonzero port")
		}
		if ip.Is6() && ip.IsLinkLocalUnicast() && ip.Zone() != listenerIP.Zone() {
			return errors.New("UDP-Lite link-local return zone must match listener scope")
		}
		counts[route.Listener+"\x00"+route.Principal]++
	}
	for _, listener := range listeners {
		if !listener.Enabled {
			continue
		}

		if c.ResponseSigningKey == nil || len(c.RecipientKeys) == 0 || len(c.Principals) == 0 {
			return errors.New("UDP-Lite requires ingress recipient keys, response signing key and principals")
		}
		for _, principal := range c.Principals {
			if len(principal.SigningKeys) == 0 || counts[listener.ID+"\x00"+principal.ID] != 1 {
				return errors.New("UDP-Lite requires a signing principal with exactly one protected return route")
			}
		}
	}
	return nil
}
