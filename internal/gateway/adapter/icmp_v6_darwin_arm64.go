//go:build darwin && arm64

package adapter

import (
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"syscall"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv6"
	"golang.org/x/sys/unix"
)

const icmpV6ReceiveBufferBytes = 256 << 10

type ICMPv6Packets struct {
	conn           *net.UDPConn
	packets        *ipv6.PacketConn
	local          ICMPv6Addr
	interfaceIndex int
}

func ListenICMPv6(address *ICMPv6Addr, interfaceName string) (*ICMPv6Packets, error) {
	if !validICMPv6Addr(address) || interfaceName == "" {
		return nil, errors.New("invalid ICMPv6 endpoint")
	}
	if err := (&config.ICMPv6{Interface: interfaceName}).Validate(address.String()); err != nil {
		return nil, err
	}
	iface, err := net.InterfaceByName(interfaceName)
	if err != nil {
		return nil, err
	}
	if iface.Flags&net.FlagUp == 0 || iface.Index <= 0 {
		return nil, errors.New("ICMPv6 interface is unavailable")
	}
	if !interfaceOwnsIPv6Address(iface, address.IP) {
		return nil, errors.New("ICMPv6 interface does not own the configured address")
	}
	conn, err := openICMPv6Socket(address.IP)
	if err != nil {
		return nil, err
	}
	packets := ipv6.NewPacketConn(conn)
	var filter ipv6.ICMPFilter
	filter.SetAll(true)
	filter.Accept(ipv6.ICMPTypeEchoRequest)
	if err := packets.SetICMPFilter(&filter); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := packets.SetControlMessage(ipv6.FlagDst|ipv6.FlagInterface, true); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &ICMPv6Packets{
		conn:           conn,
		packets:        packets,
		local:          ICMPv6Addr{IP: append(net.IP(nil), address.IP.To16()...), Channel: address.Channel},
		interfaceIndex: iface.Index,
	}, nil
}

// Darwin's default ICMP receive queue is 8 KiB, too small for a complete
// fragmented request burst. Own the descriptor to set a bounded queue before
// binding; the high-level icmp.ListenPacket API does not expose this option.
func openICMPv6Socket(address net.IP) (*net.UDPConn, error) {
	syscall.ForkLock.RLock()
	fd, err := unix.Socket(unix.AF_INET6, unix.SOCK_DGRAM, unix.IPPROTO_ICMPV6)
	if err == nil {
		_, err = unix.FcntlInt(uintptr(fd), unix.F_SETFD, unix.FD_CLOEXEC)
		if err != nil {
			_ = unix.Close(fd)
		}
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "icmp-v6")
	defer file.Close()
	if err := unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_RCVBUF, icmpV6ReceiveBufferBytes); err != nil {
		return nil, err
	}
	size, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_RCVBUF)
	if err != nil {
		return nil, err
	}
	if size != icmpV6ReceiveBufferBytes {
		return nil, errors.New("ICMPv6 receive buffer was not applied")
	}
	if err := unix.SetsockoptInt(fd, unix.IPPROTO_IPV6, unix.IPV6_V6ONLY, 1); err != nil {
		return nil, err
	}
	local := &unix.SockaddrInet6{}
	copy(local.Addr[:], address.To16())
	if err := unix.Bind(fd, local); err != nil {
		return nil, err
	}
	packets, err := net.FilePacketConn(file)
	if err != nil {
		return nil, err
	}
	conn, ok := packets.(*net.UDPConn)
	if !ok {
		_ = packets.Close()
		return nil, errors.New("ICMPv6 descriptor cannot use bounded datagram ownership")
	}
	return conn, nil
}

func interfaceOwnsIPv6Address(iface *net.Interface, expected net.IP) bool {
	addresses, err := iface.Addrs()
	if err != nil {
		return false
	}
	want := expected.To16()
	for _, address := range addresses {
		var candidate net.IP
		switch value := address.(type) {
		case *net.IPNet:
			candidate = value.IP
		case *net.IPAddr:
			candidate = value.IP
		default:
			continue
		}
		if candidate.To4() == nil && candidate.To16() != nil && candidate.To16().Equal(want) {
			return true
		}
	}
	return false
}

func validICMPv6Source(control *ipv6.ControlMessage, source net.Addr, sourceChannel int, localIP net.IP, interfaceIndex int) (*ICMPv6Addr, bool) {
	if control == nil || control.Dst == nil || !control.Dst.Equal(localIP) ||
		control.IfIndex != interfaceIndex || sourceChannel <= 0 || sourceChannel > 65535 {
		return nil, false
	}
	address, ok := source.(*net.UDPAddr)
	if !ok || address == nil || address.Zone != "" || address.IP.To4() != nil {
		return nil, false
	}
	ip, ok := netip.AddrFromSlice(address.IP)
	if !ok || !ip.Is6() || ip.Is4In6() || ip.IsUnspecified() ||
		ip.IsMulticast() || ip.IsLinkLocalUnicast() {
		return nil, false
	}
	return &ICMPv6Addr{IP: append(net.IP(nil), address.IP.To16()...), Channel: uint16(sourceChannel)}, true
}

func (c *ICMPv6Packets) ReadFrom(body []byte) (int, net.Addr, error) {
	if c == nil || c.conn == nil || c.packets == nil {
		return 0, nil, errors.New("ICMPv6 socket is closed")
	}
	var wire [8 + fragmentDatagramBytes + 1]byte
	for {
		n, control, source, err := c.packets.ReadFrom(wire[:])
		if err != nil {
			return 0, nil, err
		}
		if n <= 0 || n >= len(wire) {
			continue
		}
		message, err := icmp.ParseMessage(58, wire[:n])
		if err != nil || message.Type != ipv6.ICMPTypeEchoRequest || message.Code != 0 {
			continue
		}
		echo, ok := message.Body.(*icmp.Echo)
		if !ok || echo.ID != int(c.local.Channel) || len(echo.Data) == 0 ||
			len(echo.Data) > fragmentDatagramBytes {
			continue
		}
		peer, ok := validICMPv6Source(control, source, echo.Seq, c.local.IP, c.interfaceIndex)
		if !ok {
			continue
		}
		if len(body) < len(echo.Data) {
			continue
		}
		copy(body, echo.Data)
		return len(echo.Data), peer, nil
	}
}

func (c *ICMPv6Packets) WriteTo(body []byte, destination net.Addr) (int, error) {
	if c == nil || c.conn == nil || c.packets == nil {
		return 0, errors.New("ICMPv6 socket is closed")
	}
	peer, ok := destination.(*ICMPv6Addr)
	if !ok || !validICMPv6Addr(peer) || len(body) == 0 || len(body) > fragmentDatagramBytes {
		return 0, errors.New("invalid ICMPv6 datagram destination or size")
	}
	wire, err := (&icmp.Message{
		Type: ipv6.ICMPTypeEchoRequest,
		Code: 0,
		Body: &icmp.Echo{ID: int(peer.Channel), Seq: int(c.local.Channel), Data: body},
	}).Marshal(nil)
	if err != nil {
		return 0, err
	}
	control := &ipv6.ControlMessage{Src: c.local.IP, IfIndex: c.interfaceIndex}
	destinationAddress := &net.UDPAddr{IP: append(net.IP(nil), peer.IP.To16()...)}
	n, err := c.packets.WriteTo(wire, control, destinationAddress)
	if err != nil {
		return 0, err
	}
	if n != len(wire) {
		return 0, io.ErrShortWrite
	}
	return len(body), nil
}

func (c *ICMPv6Packets) LocalAddr() net.Addr {
	if c == nil {
		return (*ICMPv6Addr)(nil)
	}
	return &ICMPv6Addr{IP: append(net.IP(nil), c.local.IP...), Channel: c.local.Channel}
}

func (c *ICMPv6Packets) Close() error { return c.conn.Close() }

func (c *ICMPv6Packets) SetDeadline(deadline time.Time) error {
	return c.conn.SetDeadline(deadline)
}

func (c *ICMPv6Packets) SetReadDeadline(deadline time.Time) error {
	return c.conn.SetReadDeadline(deadline)
}

func (c *ICMPv6Packets) SetWriteDeadline(deadline time.Time) error {
	return c.conn.SetWriteDeadline(deadline)
}
