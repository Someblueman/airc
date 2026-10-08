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
