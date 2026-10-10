//go:build linux

package adapter

import (
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"sync"
	"time"

	"golang.org/x/net/ipv4"
	"golang.org/x/sys/unix"
)

const (
	rawIPProtocol      = 253
	rawIPHeaderBytes   = 8
	rawIPMaxDatagram   = 1000
	rawIPReceiveBuffer = 256 << 10
	rawIPWireMagic     = "AGI1"
)

// RawIPPackets owns a bounded IPv4 protocol-253 socket.
type RawIPPackets struct {
	conn       *net.IPConn
	raw        *ipv4.RawConn
	local      RawIPAddr
	ifIndex    int
	networks   []*net.IPNet
	closeOnce  sync.Once
	closeError error
}

func ListenRawIP(address *RawIPAddr, interfaceName string) (*RawIPPackets, error) {
	if !validRawIPAddr(address) || interfaceName == "" {
		return nil, errors.New("invalid raw IP listener address or interface")
	}
	iface, err := net.InterfaceByName(interfaceName)
	if err != nil {
		return nil, err
	}
	if iface.Index <= 0 || iface.Flags&net.FlagUp == 0 {
		return nil, errors.New("raw IP interface is not up")
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return nil, err
	}
	owned := false
	networks := make([]*net.IPNet, 0, len(addrs))
	for _, addr := range addrs {
		ip, network, err := net.ParseCIDR(addr.String())
		if err != nil || ip.To4() == nil || network == nil {
			continue
		}
		network.IP = network.IP.To4()
		networks = append(networks, network)
		if ip.To4().Equal(address.IP) {
			owned = true
		}
	}
	if !owned || isKnownIPv4SpecialNetworkAddress(address.IP, networks) {
		return nil, errors.New("raw IP listener address is not a usable interface unicast address")
	}

	conn, err := net.ListenIP("ip4:253", &net.IPAddr{IP: append(net.IP(nil), address.IP...)})
	if err != nil {
		return nil, err
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = conn.Close()
		}
	}()
	if err := setRawIPReceiveBuffer(conn); err != nil {
		return nil, err
	}
	raw, err := ipv4.NewRawConn(conn)
	if err != nil {
		return nil, err
	}
	if err := raw.SetControlMessage(ipv4.FlagSrc|ipv4.FlagDst|ipv4.FlagInterface, true); err != nil {
		return nil, err
	}
	closeOnError = false
	return &RawIPPackets{
		conn:     conn,
		raw:      raw,
		local:    RawIPAddr{IP: append(net.IP(nil), address.IP...), Channel: address.Channel},
		ifIndex:  iface.Index,
		networks: networks,
	}, nil
}

func setRawIPReceiveBuffer(conn *net.IPConn) error {
	control, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var socketErr error
	if err := control.Control(func(fd uintptr) {
		// Linux doubles SO_RCVBUF on readback. Request half the desired
		// ceiling so the effective receive queue remains at most 256 KiB.
		socketErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_RCVBUF, rawIPReceiveBuffer/2)
		if socketErr != nil {
			return
		}
		effective, getErr := unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_RCVBUF)
		if getErr != nil {
			socketErr = getErr
			return
		}
		if effective != rawIPReceiveBuffer {
			socketErr = errors.New("kernel raw IP receive queue does not match configured bound")
		}
	}); err != nil {
		return err
	}
	return socketErr
}

func isKnownIPv4SpecialNetworkAddress(ip net.IP, networks []*net.IPNet) bool {
	want := ip.To4()
	if want == nil {
		return false
	}
	for _, network := range networks {
		ones, bits := network.Mask.Size()
		if bits != 32 || ones >= 31 {
			continue
		}
		if network.IP.To4().Equal(want) {
			return true
		}
		broadcast := append(net.IP(nil), network.IP.To4()...)
		for index := range broadcast {
			broadcast[index] |= ^network.Mask[index]
		}
		if broadcast.Equal(want) {
			return true
		}
	}
	return false
}

func (c *RawIPPackets) ReadFrom(body []byte) (int, net.Addr, error) {
	if c == nil || c.raw == nil || c.conn == nil {
		return 0, nil, net.ErrClosed
	}
	// The largest legal IPv4 datagram is 65535 bytes including its header.
	// A larger-than-protocol bound also makes truncation detectable by length.
	var wire [65536]byte
	for {
		header, payload, control, err := c.raw.ReadFrom(wire[:])
		if err != nil {
			return 0, nil, err
		}
		if !c.validInbound(header, payload, control) {
			continue
		}
		data := payload[rawIPHeaderBytes:]
		if len(body) < len(data) {
			return 0, nil, errors.New("raw IP read buffer is too small for complete datagram")
		}
		copy(body, data)
		return len(data), &RawIPAddr{IP: append(net.IP(nil), header.Src.To4()...), Channel: binary.BigEndian.Uint16(payload[6:8])}, nil
	}
}

func (c *RawIPPackets) validInbound(header *ipv4.Header, payload []byte, control *ipv4.ControlMessage) bool {
	if header == nil || control == nil || header.Version != 4 || header.Protocol != rawIPProtocol ||
		header.Len != ipv4.HeaderLen || header.TotalLen != header.Len+len(payload) ||
		header.Flags&^ipv4.DontFragment != 0 || header.FragOff != 0 ||
		!header.Dst.To4().Equal(c.local.IP) || control.Dst == nil || !control.Dst.Equal(c.local.IP) ||
		control.IfIndex != c.ifIndex || len(payload) < rawIPHeaderBytes+1 ||
		len(payload) > rawIPHeaderBytes+rawIPMaxDatagram {
		return false
	}
	if !validRawIPv4Peer(header.Src) || isKnownIPv4SpecialNetworkAddress(header.Src, c.networks) ||
		string(payload[:4]) != rawIPWireMagic || binary.BigEndian.Uint16(payload[4:6]) != c.local.Channel ||
		binary.BigEndian.Uint16(payload[6:8]) == 0 {
		return false
	}
	return true
}

func validRawIPv4Peer(ip net.IP) bool {
	ipv4 := ip.To4()
	addr, ok := netip.AddrFromSlice(ipv4)
	return ok && addr.Is4() && !addr.IsUnspecified() && !addr.IsMulticast() &&
		addr != netip.MustParseAddr("255.255.255.255")
}

func (c *RawIPPackets) WriteTo(body []byte, destination net.Addr) (int, error) {
	if c == nil || c.raw == nil || c.conn == nil {
		return 0, net.ErrClosed
	}
	peer, ok := destination.(*RawIPAddr)
	if !ok || !validRawIPAddr(peer) || len(body) == 0 || len(body) > rawIPMaxDatagram ||
		isKnownIPv4SpecialNetworkAddress(peer.IP, c.networks) {
		return 0, errors.New("invalid raw IP destination or datagram size")
	}
	if peer.IP.Equal(c.local.IP) && peer.Channel == c.local.Channel {
		return 0, errors.New("raw IP destination cannot be the listener endpoint")
	}
	payload := make([]byte, rawIPHeaderBytes+len(body))
	copy(payload[:4], rawIPWireMagic)
	binary.BigEndian.PutUint16(payload[4:6], peer.Channel)
	binary.BigEndian.PutUint16(payload[6:8], c.local.Channel)
	copy(payload[rawIPHeaderBytes:], body)
	header := &ipv4.Header{
		Version: 4, Len: ipv4.HeaderLen, TotalLen: ipv4.HeaderLen + len(payload),
		Flags: ipv4.DontFragment, TTL: 64, Protocol: rawIPProtocol,
		Src: c.local.IP, Dst: peer.IP,
	}
	control := &ipv4.ControlMessage{Src: c.local.IP, IfIndex: c.ifIndex}
	if err := c.raw.WriteTo(header, payload, control); err != nil {
		return 0, err
	}
	return len(body), nil
}

func (c *RawIPPackets) LocalAddr() net.Addr {
	if c == nil {
		return (*RawIPAddr)(nil)
	}
	return &RawIPAddr{IP: append(net.IP(nil), c.local.IP...), Channel: c.local.Channel}
}

func (c *RawIPPackets) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	c.closeOnce.Do(func() { c.closeError = c.conn.Close() })
	return c.closeError
}

func (c *RawIPPackets) SetDeadline(deadline time.Time) error {
	if c == nil || c.conn == nil {
		return net.ErrClosed
	}
	return c.conn.SetDeadline(deadline)
}

func (c *RawIPPackets) SetReadDeadline(deadline time.Time) error {
	if c == nil || c.conn == nil {
		return net.ErrClosed
	}
	return c.conn.SetReadDeadline(deadline)
}

func (c *RawIPPackets) SetWriteDeadline(deadline time.Time) error {
	if c == nil || c.conn == nil {
		return net.ErrClosed
	}
	return c.conn.SetWriteDeadline(deadline)
}

var _ net.PacketConn = (*RawIPPackets)(nil)
