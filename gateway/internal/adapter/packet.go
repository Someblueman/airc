package adapter

import (
	"net"
	"path/filepath"
	"time"
)

const (
	packetSourceIdle = 5 * time.Second
	udpErrorReserve  = 4 << 10
	udpWireBudget    = 1 << 20
)

type packetSource struct {
	last     time.Time
	partial  *datagramAssembler
	active   map[string]struct{}
	received int // wire bytes of the request being assembled (author replies)
}

func validPacketDestination(addr net.Addr) bool {
	switch a := addr.(type) {
	case *net.UDPAddr:
		if a == nil || a.Port <= 0 || a.Port > 65535 || a.IP == nil || a.IP.To16() == nil || a.IP.IsUnspecified() || a.IP.IsMulticast() || a.IP.Equal(net.IPv4bcast) {
			return false
		}
		return true
	case *net.UnixAddr:
		return a != nil && a.Net == "unixgram" && filepath.IsAbs(a.Name) && filepath.Clean(a.Name) == a.Name && len(a.Name) <= 108
	default:
		return false
	}
}

func clonePacketAddr(addr net.Addr) net.Addr {
	switch a := addr.(type) {
	case *net.UDPAddr:
		if a == nil {
			return (*net.UDPAddr)(nil)
		}
		return &net.UDPAddr{IP: append(net.IP(nil), a.IP...), Port: a.Port, Zone: a.Zone}
	case *net.UnixAddr:
		if a == nil {
			return (*net.UnixAddr)(nil)
		}
		return &net.UnixAddr{Name: a.Name, Net: a.Net}
	default:
		return nil
	}
}

func packetAddrKey(addr net.Addr) string {
	if addr == nil {
		return "unnamed:"
	}
	switch a := addr.(type) {
	case *net.UDPAddr:
		if a == nil {
			return "unnamed:udp"
		}
		return "udp:" + a.String()
	case *net.UnixAddr:
		if a == nil {
			return "unnamed:unix"
		}
		return "unix:" + a.String()
	default:
		return "other:" + addr.String()
	}
}

func packetWatchMode(conn net.PacketConn) string {
	if conn != nil && conn.LocalAddr() != nil && conn.LocalAddr().Network() == "unixgram" {
		return "unixgram_mailbox_datagrams"
	}
	return "udp_mailbox_datagrams"
}
