package adapter

import (
	"net"
	"net/netip"
	"strconv"

	"github.com/Someblueman/airc/internal/gateway/config"
)

// ICMPv6Addr identifies a peer by its IPv6 address and cooperative ICMP echo
// channel. The channel is carried in Echo.ID; it is not an OS-exclusive bind.
type ICMPv6Addr struct {
	IP      net.IP
	Channel uint16
}

func (a *ICMPv6Addr) Network() string { return "icmp_v6" }

func (a *ICMPv6Addr) String() string {
	if a == nil {
		return "<nil>"
	}
	return net.JoinHostPort(a.IP.String(), strconv.Itoa(int(a.Channel)))
}

// ParseICMPv6Addr parses the canonical literal IPv6 address/channel endpoint.
func ParseICMPv6Addr(address string) (*ICMPv6Addr, error) {
	ip, channel, err := config.ParseICMPv6Endpoint(address)
	if err != nil {
		return nil, err
	}
	return &ICMPv6Addr{IP: net.IP(ip.AsSlice()), Channel: channel}, nil
}

func validICMPv6Addr(address *ICMPv6Addr) bool {
	if address == nil || address.Channel == 0 || address.IP.To4() != nil {
		return false
	}
	ip, ok := netip.AddrFromSlice(address.IP)
	if !ok || !ip.Is6() || ip.Is4In6() || ip.IsUnspecified() ||
		ip.IsMulticast() || ip.IsLinkLocalUnicast() {
		return false
	}
	_, err := ParseICMPv6Addr(address.String())
	return err == nil
}
