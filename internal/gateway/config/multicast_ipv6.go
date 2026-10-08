package config

import (
	"errors"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

// IPv6Multicast pins one local interface; group scope grants no sender authority.
type IPv6Multicast struct {
	Interface string `json:"interface"`
}

// Validate requires a canonical transient group with a defined multicast scope.
// The interface supplies the zone; an address cannot override it.
func (options *IPv6Multicast) Validate(address string) error {
	if options == nil || options.Interface == "" || len(options.Interface) > 64 || strings.TrimSpace(options.Interface) != options.Interface || strings.ContainsAny(options.Interface, "/\\\x00\r\n\t ") {
		return errors.New("invalid IPv6 multicast interface")
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return errors.New("invalid IPv6 multicast endpoint")
	}
	group, err := netip.ParseAddr(host)
	if err != nil || !group.Is6() || !group.IsMulticast() || group.Zone() != "" {
		return errors.New("IPv6 multicast requires a literal group without an address zone")
	}
	bytes := group.As16()
	// Plain transient groups avoid reserved permanent and embedded-prefix formats.
	if bytes[1]>>4 != 1 {
		return errors.New("IPv6 multicast requires a plain transient group")
	}
	switch bytes[1] & 15 {
	case 1, 2, 3, 4, 5, 8, 14:
	default:
		return errors.New("IPv6 multicast requires a defined scope")
	}
	var nonzero byte
	for _, b := range bytes[2:] {
		nonzero |= b
	}
	if nonzero == 0 {
		return errors.New("IPv6 multicast requires a nonzero group identifier")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port || net.JoinHostPort(group.String(), port) != address {
		return errors.New("invalid IPv6 multicast port or spelling")
	}
	return nil
}
