package adapter

import (
	"net"
	"net/netip"
	"strconv"

	"github.com/Someblueman/airc/internal/gateway/config"
)

// DCCPAddr identifies an IPv4 DCCP endpoint.
type DCCPAddr struct {
	IP   net.IP
	Port int
}

func ParseDCCPAddr(endpoint string) (*DCCPAddr, error) {
	ip, port, err := config.ParseDCCPEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	return &DCCPAddr{IP: net.IP(ip.AsSlice()), Port: int(port)}, nil
}

func (a *DCCPAddr) Network() string { return "dccp" }

func (a *DCCPAddr) String() string {
	if a == nil {
		return "<nil>"
	}
	ip := a.IP.To4()
	if ip == nil {
		return net.JoinHostPort("0.0.0.0", strconv.Itoa(a.Port))
	}
	return net.JoinHostPort(ip.String(), strconv.Itoa(a.Port))
}

func validDCCPAddr(address *DCCPAddr) bool {
	if address == nil || address.Port < 1 || address.Port > 65535 {
		return false
	}
	ip, ok := netip.AddrFromSlice(address.IP)
	return ok && ip.Is4() && !ip.IsUnspecified() && !ip.IsMulticast() && ip != netip.MustParseAddr("255.255.255.255")
}
