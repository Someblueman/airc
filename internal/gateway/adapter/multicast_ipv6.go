package adapter

import (
	"errors"
	"net"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"golang.org/x/net/ipv6"
)

// IPv6MulticastPackets owns one membership and exposes only unicast writes.
// Group membership and packet source addresses do not establish sender trust.
type IPv6MulticastPackets struct {
	conn           *net.UDPConn
	packets        *ipv6.PacketConn
	group          net.UDPAddr
	interfaceIndex int
	interfaceName  string
}

// ListenIPv6Multicast requires an explicit available interface and literal group.
func ListenIPv6Multicast(group *net.UDPAddr, interfaceName string) (*IPv6MulticastPackets, error) {
	if group == nil || group.Zone != "" {
		return nil, errors.New("invalid IPv6 multicast membership")
	}
	if err := (&config.IPv6Multicast{Interface: interfaceName}).Validate(group.String()); err != nil {
		return nil, err
	}
	iface, err := net.InterfaceByName(interfaceName)
	if err != nil {
		return nil, err
	}
	if iface.Flags&net.FlagUp == 0 || iface.Index <= 0 {
		return nil, errors.New("IPv6 multicast interface is unavailable")
	}
	conn, err := net.ListenMulticastUDP("udp6", iface, group)
	if err != nil {
		return nil, err
	}

	packets := ipv6.NewPacketConn(conn)
	if err := packets.SetControlMessage(ipv6.FlagDst|ipv6.FlagInterface, true); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &IPv6MulticastPackets{conn: conn, packets: packets, group: net.UDPAddr{IP: append(net.IP(nil), group.IP.To16()...), Port: group.Port, Zone: iface.Name}, interfaceIndex: iface.Index, interfaceName: iface.Name}, nil
}

func (c *IPv6MulticastPackets) ReadFrom(body []byte) (int, net.Addr, error) {
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

func (c *IPv6MulticastPackets) WriteTo(body []byte, destination net.Addr) (int, error) {
	addr, ok := destination.(*net.UDPAddr)
	if !ok || addr == nil || addr.IP.To16() == nil || addr.IP.To4() != nil || addr.IP.IsUnspecified() || addr.IP.IsMulticast() || addr.Port < 1 || addr.Port > 65535 {
		return 0, errors.New("IPv6 multicast replies require IPv6 unicast")
	}
	if (addr.IP.IsLinkLocalUnicast() && addr.Zone != c.interfaceName) || (!addr.IP.IsLinkLocalUnicast() && addr.Zone != "") {
		return 0, errors.New("IPv6 reply zone does not match interface scope")
	}
	return c.conn.WriteToUDP(body, addr)
}

func (c *IPv6MulticastPackets) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: append(net.IP(nil), c.group.IP...), Port: c.group.Port, Zone: c.group.Zone}
}
func (c *IPv6MulticastPackets) Close() error                       { return c.conn.Close() }
func (c *IPv6MulticastPackets) SetDeadline(t time.Time) error      { return c.conn.SetDeadline(t) }
func (c *IPv6MulticastPackets) SetReadDeadline(t time.Time) error  { return c.conn.SetReadDeadline(t) }
func (c *IPv6MulticastPackets) SetWriteDeadline(t time.Time) error { return c.conn.SetWriteDeadline(t) }
