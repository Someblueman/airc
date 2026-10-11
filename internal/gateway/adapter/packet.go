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
	case *SystemVQueueAddr:
		return a != nil && a.Key > 0
	case *POSIXQueueAddr:
		return a != nil && ValidPOSIXQueueName(a.Name)
	case *SharedMemoryAddr:
		return a != nil && ValidSharedMemoryName(a.Name)
	case *MappedFileAddr:
		return a != nil && ValidMappedFileDirectory(a.Directory)
	case *ICMPv6Addr:
		return validICMPv6Addr(a)
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
	case *SystemVQueueAddr:
		if a == nil {
			return (*SystemVQueueAddr)(nil)
		}
		return &SystemVQueueAddr{Key: a.Key}
	case *POSIXQueueAddr:
		if a == nil {
			return (*POSIXQueueAddr)(nil)
		}
		return &POSIXQueueAddr{Name: a.Name}
	case *SharedMemoryAddr:
		if a == nil {
			return (*SharedMemoryAddr)(nil)
		}
		return &SharedMemoryAddr{Name: a.Name}
	case *MappedFileAddr:
		if a == nil {
			return (*MappedFileAddr)(nil)
		}
		return &MappedFileAddr{Directory: a.Directory}
	case *ICMPv6Addr:
		if a == nil {
			return (*ICMPv6Addr)(nil)
		}
		return &ICMPv6Addr{IP: append(net.IP(nil), a.IP...), Channel: a.Channel}
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
	if conn != nil && conn.LocalAddr() != nil && conn.LocalAddr().Network() == "mapped_file" {
		return "mapped_file_mailbox_fragments"
	}
	if conn != nil && conn.LocalAddr() != nil && conn.LocalAddr().Network() == "shared_memory" {
		return "shared_memory_mailbox_fragments"
	}
	if conn != nil && conn.LocalAddr() != nil && conn.LocalAddr().Network() == "system_v_queue" {
		return "system_v_queue_mailbox_fragments"
	}
	if conn != nil && conn.LocalAddr() != nil && conn.LocalAddr().Network() == "posix_mqueue" {
		return "posix_queue_mailbox_fragments"
	}
	if conn != nil && conn.LocalAddr() != nil && conn.LocalAddr().Network() == "icmp_v6" {
		return "icmp_v6_mailbox_datagrams"
	}
	if _, ok := conn.(*IPv4BroadcastPackets); ok {
		return "ipv4_broadcast_unicast_mailbox_datagrams"
	}
	if conn != nil && conn.LocalAddr() != nil && conn.LocalAddr().Network() == "unixgram" {
		return "unixgram_mailbox_datagrams"
	}
	return "udp_mailbox_datagrams"
}
