package adapter

import (
	"errors"
	"github.com/Someblueman/airc/internal/gateway/config"
	"net"
	"net/netip"
	"time"

	"golang.org/x/net/ipv4"
)

// IPv4BroadcastPackets owns one membership and exposes only unicast writes.
// Group membership and packet source addresses do not establish sender trust.
type IPv4BroadcastPackets struct {
	conn           *net.UDPConn
	packets        *ipv4.PacketConn
	group          net.UDPAddr
	interfaceIndex int
	options        config.IPv4Broadcast
}

// ListenIPv4Broadcast requires an explicit available interface and literal group.
func ListenIPv4Broadcast(group *net.UDPAddr, interfaceName, subnetText string) (*IPv4BroadcastPackets, error) {
	if group == nil || group.Zone != "" {
		return nil, errors.New("invalid IPv4 broadcast endpoint")
	}
	if err := (&config.IPv4Broadcast{Interface: interfaceName, Subnet: subnetText}).Validate(group.String()); err != nil {
		return nil, err
	}
	iface, err := net.InterfaceByName(interfaceName)
	if err != nil {
		return nil, err
	}
	if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagBroadcast == 0 || iface.Index <= 0 {
		return nil, errors.New("IPv4 broadcast interface is unavailable")
	}
	subnet, _ := netip.ParsePrefix(subnetText)
	addresses, err := iface.Addrs()
	if err != nil {
		return nil, err
	}
	owned := false
	for _, address := range addresses {
		prefix, err := netip.ParsePrefix(address.String())
		if err == nil && prefix.Addr().Is4() && prefix.Masked() == subnet {
			owned = true
			break
		}
	}
	if !owned {
		return nil, errors.New("IPv4 broadcast interface does not own the configured subnet")
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: group.Port})
	if err != nil {
		return nil, err
	}
	if err := disableIPv4MulticastBroadcast(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	packets := ipv4.NewPacketConn(conn)
	if err := packets.SetControlMessage(ipv4.FlagDst|ipv4.FlagInterface, true); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &IPv4BroadcastPackets{conn: conn, packets: packets, group: net.UDPAddr{IP: append(net.IP(nil), group.IP.To4()...), Port: group.Port}, interfaceIndex: iface.Index, options: config.IPv4Broadcast{Interface: interfaceName, Subnet: subnetText}}, nil
}

func (c *IPv4BroadcastPackets) ReadFrom(body []byte) (int, net.Addr, error) {
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

func (c *IPv4BroadcastPackets) WriteTo(body []byte, destination net.Addr) (int, error) {
	addr, ok := destination.(*net.UDPAddr)
	if !ok || addr == nil || addr.IP.To4() == nil || addr.IP.IsUnspecified() || addr.IP.IsMulticast() || addr.IP.Equal(net.IPv4bcast) || addr.Port < 1 || addr.Port > 65535 || addr.Zone != "" || c.options.Validate(addr.String()) == nil {
		return 0, errors.New("IPv4 broadcast replies require IPv4 unicast")
	}
	return c.conn.WriteToUDP(body, addr)
}

func (c *IPv4BroadcastPackets) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: append(net.IP(nil), c.group.IP...), Port: c.group.Port}
}
func (c *IPv4BroadcastPackets) Close() error                       { return c.conn.Close() }
func (c *IPv4BroadcastPackets) SetDeadline(t time.Time) error      { return c.conn.SetDeadline(t) }
func (c *IPv4BroadcastPackets) SetReadDeadline(t time.Time) error  { return c.conn.SetReadDeadline(t) }
func (c *IPv4BroadcastPackets) SetWriteDeadline(t time.Time) error { return c.conn.SetWriteDeadline(t) }
