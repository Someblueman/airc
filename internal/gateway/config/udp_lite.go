package config

import (
	"errors"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

// UDPLite requires full transport checksum coverage in addition to authenticated framing.
type UDPLite struct {
	ChecksumCoverage string `json:"checksum_coverage"`
}

// Validate makes integrity policy explicit and prevents hostname or address ambiguity.
func (options *UDPLite) Validate(address string) error {
	if options == nil || options.ChecksumCoverage != "full" {
		return errors.New("UDP-Lite requires explicit full checksum coverage")
	}
	host, port, err := net.SplitHostPort(address)
	ip, ipErr := netip.ParseAddr(host)
	n, portErr := strconv.Atoi(port)
	if err != nil || ipErr != nil || portErr != nil || ip.Is4In6() || ip.IsMulticast() || ip == netip.MustParseAddr("255.255.255.255") || n < 0 || n > 65535 || strconv.Itoa(n) != port || net.JoinHostPort(ip.String(), port) != address {
		return errors.New("invalid UDP-Lite numeric endpoint")
	}
	zone := ip.Zone()
	if ip.Is6() && ip.IsLinkLocalUnicast() {
		if zone == "" || len(zone) > 64 || strings.ContainsAny(zone, "/\\\x00\r\n\t ") {
			return errors.New("UDP-Lite link-local endpoint requires an explicit interface zone")
		}
	} else if zone != "" {
		return errors.New("UDP-Lite endpoint cannot specify an unrelated zone")
	}
	return nil
}
