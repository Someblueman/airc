package config

import (
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

// IPv4Broadcast pins the local interface and its canonical broadcast subnet.
// Neither a subnet nor a packet source grants sender authority.
type IPv4Broadcast struct {
	Interface string `json:"interface"`
	Subnet    string `json:"subnet"`
}

// Validate accepts limited broadcast or the selected subnet's directed broadcast.
// Runtime ownership must also verify that the interface actually owns this subnet.
func (options *IPv4Broadcast) Validate(address string) error {
	if options == nil || options.Interface == "" || len(options.Interface) > 64 || strings.TrimSpace(options.Interface) != options.Interface || strings.ContainsAny(options.Interface, "/\\\x00\r\n\t ") {
		return errors.New("invalid IPv4 broadcast interface")
	}
	subnet, err := netip.ParsePrefix(options.Subnet)
	if err != nil || !subnet.Addr().Is4() || subnet.Bits() < 1 || subnet.Bits() > 30 || subnet.Masked() != subnet || subnet.String() != options.Subnet || subnet.Addr().IsUnspecified() || subnet.Addr().IsMulticast() {
		return errors.New("IPv4 broadcast requires a canonical broadcast-capable subnet")
	}
	host, port, err := net.SplitHostPort(address)
	ip, ipErr := netip.ParseAddr(host)
	n, portErr := strconv.Atoi(port)
	if err != nil || ipErr != nil || portErr != nil || !ip.Is4() || n < 1 || n > 65535 || strconv.Itoa(n) != port || net.JoinHostPort(ip.String(), port) != address {
		return errors.New("invalid IPv4 broadcast endpoint")
	}
	bytes := subnet.Addr().As4()
	value := binary.BigEndian.Uint32(bytes[:]) | (uint32(1)<<uint(32-subnet.Bits()) - 1)
	binary.BigEndian.PutUint32(bytes[:], value)
	directed := netip.AddrFrom4(bytes)
	if ip != directed && ip != netip.MustParseAddr("255.255.255.255") {
		return errors.New("IPv4 broadcast endpoint does not match selected subnet")
	}
	return nil
}
