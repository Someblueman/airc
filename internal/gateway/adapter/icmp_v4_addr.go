package adapter

import (
	"net"
	"net/netip"
	"strconv"

	"github.com/Someblueman/airc/internal/gateway/config"
)

// ICMPv4Addr identifies an IPv4 echo peer and its application channel.
type ICMPv4Addr struct {
	IP      net.IP
	Channel uint16
}

func ParseICMPv4Addr(address string) (*ICMPv4Addr, error) {
	ip, channel, err := config.ParseICMPv4Endpoint(address)
	if err != nil {
		return nil, err
	}
	return &ICMPv4Addr{IP: net.IP(ip.AsSlice()), Channel: channel}, nil
}

func (a *ICMPv4Addr) Network() string { return "icmp_v4" }

func (a *ICMPv4Addr) String() string {
	if a == nil {
		return "<nil>"
	}
	ip := a.IP.To4()
	if ip == nil {
		return net.JoinHostPort("0.0.0.0", strconv.Itoa(int(a.Channel)))
	}
	return net.JoinHostPort(ip.String(), strconv.Itoa(int(a.Channel)))
}

func validICMPv4Addr(address *ICMPv4Addr) bool {
	if address == nil || address.Channel == 0 {
		return false
	}
	ip, ok := netip.AddrFromSlice(address.IP)
	return ok && ip.Is4() && !ip.IsUnspecified() && !ip.IsMulticast() &&
		ip != netip.MustParseAddr("255.255.255.255")
}
