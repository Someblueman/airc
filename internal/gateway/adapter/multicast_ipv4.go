package adapter

import (
	"errors"
	"net"
	"time"

	"golang.org/x/net/ipv4"
)

// IPv4MulticastPackets owns one membership and exposes only unicast writes.
// Group membership and packet source addresses do not establish sender trust.
type IPv4MulticastPackets struct {
	conn           *net.UDPConn
	packets        *ipv4.PacketConn
	group          net.UDPAddr
	interfaceIndex int
}

// ListenIPv4Multicast requires an explicit available interface and literal group.
func ListenIPv4Multicast(group *net.UDPAddr, interfaceName string) (*IPv4MulticastPackets, error) {
	if group == nil || group.IP.To4() == nil || group.IP.To4()[0] != 239 || group.Port < 1 || group.Port > 65535 || group.Zone != "" || interfaceName == "" {
		return nil, errors.New("invalid IPv4 multicast membership")
	}
	iface, err := net.InterfaceByName(interfaceName)
	if err != nil {
		return nil, err
	}
	if iface.Flags&net.FlagUp == 0 || iface.Index <= 0 {
		return nil, errors.New("IPv4 multicast interface is unavailable")
	}
	conn, err := net.ListenMulticastUDP("udp4", iface, group)
	if err != nil {
		return nil, err
	}
	packets := ipv4.NewPacketConn(conn)
	if err := packets.SetControlMessage(ipv4.FlagDst|ipv4.FlagInterface, true); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &IPv4MulticastPackets{conn: conn, packets: packets, group: net.UDPAddr{IP: append(net.IP(nil), group.IP.To4()...), Port: group.Port}, interfaceIndex: iface.Index}, nil
}

func (c *IPv4MulticastPackets) ReadFrom(body []byte) (int, net.Addr, error) {
	for {
		n, control, source, err := c.packets.ReadFrom(body)
		if err != nil {
			return 0, nil, err
		}
		if control != nil && control.IfIndex == c.interfaceIndex && control.Dst.Equal(c.group.IP) {
			return n, source, nil
		}
	}
}

func (c *IPv4MulticastPackets) WriteTo(body []byte, destination net.Addr) (int, error) {
	addr, ok := destination.(*net.UDPAddr)
	if !ok || addr == nil || addr.IP.To4() == nil || addr.IP.IsUnspecified() || addr.IP.IsMulticast() || addr.IP.Equal(net.IPv4bcast) || addr.Port < 1 || addr.Port > 65535 || addr.Zone != "" {
		return 0, errors.New("IPv4 multicast replies require IPv4 unicast")
	}
	return c.conn.WriteToUDP(body, addr)
}

func (c *IPv4MulticastPackets) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: append(net.IP(nil), c.group.IP...), Port: c.group.Port}
}
func (c *IPv4MulticastPackets) Close() error                       { return c.conn.Close() }
func (c *IPv4MulticastPackets) SetDeadline(t time.Time) error      { return c.conn.SetDeadline(t) }
func (c *IPv4MulticastPackets) SetReadDeadline(t time.Time) error  { return c.conn.SetReadDeadline(t) }
func (c *IPv4MulticastPackets) SetWriteDeadline(t time.Time) error { return c.conn.SetWriteDeadline(t) }
