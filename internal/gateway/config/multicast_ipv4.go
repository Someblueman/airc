package config

import (
	"errors"
	"net"
	"net/netip"
	"runtime"
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
	for _, listener := range listeners {
		if !listener.Enabled {
			continue
		}
		if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
			return errors.New("IPv4 multicast requires Darwin or Linux socket ownership")
		}
	}
	return nil
}
