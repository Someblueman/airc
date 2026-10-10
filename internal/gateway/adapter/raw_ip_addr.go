package adapter

import (
	"net"
	"net/netip"
	"strconv"

	"github.com/Someblueman/airc/internal/gateway/config"
)

// RawIPAddr identifies an IPv4 raw-IP peer and its application channel.
// Channels are framing identifiers, not transport ports or authentication.
type RawIPAddr struct {
	IP      net.IP
	Channel uint16
}

func ParseRawIPAddr(address string) (*RawIPAddr, error) {
	ip, channel, err := config.ParseRawIPEndpoint(address)
	if err != nil {
		return nil, err
	}
	return &RawIPAddr{IP: net.IP(ip.AsSlice()), Channel: channel}, nil
}

func (a *RawIPAddr) Network() string { return "raw_ip" }

func (a *RawIPAddr) String() string {
	if a == nil {
		return "<nil>"
	}
	ip := a.IP.To4()
	if ip == nil {
		return net.JoinHostPort("0.0.0.0", strconv.Itoa(int(a.Channel)))
	}
	return net.JoinHostPort(ip.String(), strconv.Itoa(int(a.Channel)))
}

func validRawIPAddr(address *RawIPAddr) bool {
	if address == nil || address.Channel == 0 {
		return false
	}
	ip, ok := netip.AddrFromSlice(address.IP)
	return ok && ip.Is4() && !ip.IsUnspecified() && !ip.IsMulticast() &&
		ip != netip.MustParseAddr("255.255.255.255")
}
