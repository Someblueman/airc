package adapter

import (
	"errors"
	"io"
	"net"
	"net/netip"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"golang.org/x/net/ipv4"
)

// IPv4BroadcastPackets owns one broadcast endpoint and exposes only unicast writes.
// Broadcast reception and packet source addresses do not establish sender trust.
type IPv4BroadcastPackets struct {
	conn           *net.UDPConn
	packets        *ipv4.PacketConn
	group          net.UDPAddr
	sourceIP       net.IP
	interfaceIndex int
	subnet         netip.Prefix
}

const (
	ipv4BroadcastMaxDatagram = 1000
	ipv4BroadcastReadBuffer  = 128 << 10
)

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
	var sourceIP net.IP
	for _, address := range addresses {
		prefix, err := netip.ParsePrefix(address.String())
		if err == nil && prefix.Addr().Is4() && prefix.Masked() == subnet {
			candidate := prefix.Addr()
			if !candidate.IsUnspecified() && !candidate.IsMulticast() && !isSubnetNetworkOrBroadcast(candidate, subnet) {
				sourceIP = net.IP(candidate.AsSlice())
				break
			}
		}
	}
	if sourceIP == nil {
		return nil, errors.New("IPv4 broadcast interface does not own the configured subnet")
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: group.Port})
	if err != nil {
		return nil, err
	}
	// Linux doubles SO_RCVBUF, so this request caps the effective queue at
	// roughly 256 KiB there; kernels that do not double it use less memory.
	if err := conn.SetReadBuffer(ipv4BroadcastReadBuffer); err != nil {
		_ = conn.Close()
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
	return &IPv4BroadcastPackets{conn: conn, packets: packets, group: net.UDPAddr{IP: append(net.IP(nil), group.IP.To4()...), Port: group.Port}, sourceIP: append(net.IP(nil), sourceIP...), interfaceIndex: iface.Index, subnet: subnet}, nil
}

func (c *IPv4BroadcastPackets) ReadFrom(body []byte) (int, net.Addr, error) {
	if c == nil || c.packets == nil {
		return 0, nil, net.ErrClosed
	}
	var packet [ipv4BroadcastMaxDatagram + 1]byte
	for {
		n, control, source, err := c.packets.ReadFrom(packet[:])
		if err != nil {
			return 0, nil, err
		}
		if n == 0 || n > ipv4BroadcastMaxDatagram || control == nil || control.Dst == nil || control.IfIndex != c.interfaceIndex || !control.Dst.Equal(c.group.IP) {
			continue
		}
		peer, ok := source.(*net.UDPAddr)
		if !ok || peer == nil || peer.IP.To4() == nil || peer.IP.IsUnspecified() || peer.IP.IsMulticast() || peer.IP.Equal(net.IPv4bcast) || peer.Port < 1 || peer.Port > 65535 || peer.Zone != "" {
			continue
		}
		peerIP, ok := netip.AddrFromSlice(peer.IP.To4())
		if !ok || isSubnetNetworkOrBroadcast(peerIP, c.subnet) {
			continue
		}
		if len(body) < n {
			return 0, nil, io.ErrShortBuffer
		}
		copy(body, packet[:n])
		return n, &net.UDPAddr{IP: append(net.IP(nil), peer.IP.To4()...), Port: peer.Port}, nil
	}
}

func (c *IPv4BroadcastPackets) WriteTo(body []byte, destination net.Addr) (int, error) {
	if c == nil {
		return 0, errors.New("IPv4 broadcast replies require IPv4 unicast")
	}
	addr, ok := destination.(*net.UDPAddr)
	if len(body) == 0 || len(body) > ipv4BroadcastMaxDatagram || !ok || addr == nil || addr.IP.To4() == nil || addr.IP.IsUnspecified() || addr.IP.IsMulticast() || addr.IP.Equal(net.IPv4bcast) || addr.Port < 1 || addr.Port > 65535 || addr.Zone != "" {
		return 0, errors.New("IPv4 broadcast replies require IPv4 unicast")
	}
	peerIP, _ := netip.AddrFromSlice(addr.IP.To4())
	if !c.subnet.IsValid() || isSubnetNetworkOrBroadcast(peerIP, c.subnet) {
		return 0, errors.New("IPv4 broadcast replies require IPv4 unicast")
	}
	if c.packets == nil {
		return 0, net.ErrClosed
	}
	return c.packets.WriteTo(body, &ipv4.ControlMessage{Src: c.sourceIP, IfIndex: c.interfaceIndex}, addr)
}

func isSubnetNetworkOrBroadcast(address netip.Addr, subnet netip.Prefix) bool {
	if !address.Is4() || !subnet.IsValid() || !subnet.Addr().Is4() {
		return true
	}
	network := subnet.Masked().Addr()
	if address == network {
		return true
	}
	broadcast := network.As4()
	mask := net.CIDRMask(subnet.Bits(), 32)
	for i := range broadcast {
		broadcast[i] |= ^mask[i]
	}
	return address == netip.AddrFrom4(broadcast)
}

func (c *IPv4BroadcastPackets) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: append(net.IP(nil), c.group.IP...), Port: c.group.Port}
}
func (c *IPv4BroadcastPackets) Close() error                       { return c.conn.Close() }
func (c *IPv4BroadcastPackets) SetDeadline(t time.Time) error      { return c.conn.SetDeadline(t) }
func (c *IPv4BroadcastPackets) SetReadDeadline(t time.Time) error  { return c.conn.SetReadDeadline(t) }
func (c *IPv4BroadcastPackets) SetWriteDeadline(t time.Time) error { return c.conn.SetWriteDeadline(t) }
