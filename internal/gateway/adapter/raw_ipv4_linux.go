//go:build linux

package adapter

import (
	"errors"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"golang.org/x/net/ipv4"
	"golang.org/x/sys/unix"
)

const rawIPv4ReceiveBuffer = 256 << 10

// rawIPv4Socket owns the Linux raw socket mechanics shared by protocol 253
// and ICMPv4. It does not interpret either protocol's payload.
type rawIPv4Socket struct {
	conn       *net.IPConn
	raw        *ipv4.RawConn
	localIP    net.IP
	ifIndex    int
	networks   []*net.IPNet
	protocol   int
	closeOnce  sync.Once
	closeError error
}

func listenRawIPv4(localIP net.IP, interfaceName string, protocol int) (*rawIPv4Socket, error) {
	ip, ok := netip.AddrFromSlice(localIP)
	if !ok || !ip.Is4() || ip.IsUnspecified() || ip.IsMulticast() || ip == netip.MustParseAddr("255.255.255.255") || interfaceName == "" || protocol < 1 || protocol > 255 {
		return nil, errors.New("invalid raw IPv4 socket endpoint")
	}
	iface, err := net.InterfaceByName(interfaceName)
	if err != nil {
		return nil, err
	}
	if iface.Index <= 0 || iface.Flags&net.FlagUp == 0 {
		return nil, errors.New("raw IPv4 interface is not up")
	}
	addresses, err := iface.Addrs()
	if err != nil {
		return nil, err
	}
	owned := false
	networks := make([]*net.IPNet, 0, len(addresses))
	for _, address := range addresses {
		candidate, network, err := net.ParseCIDR(address.String())
		if err != nil || candidate.To4() == nil || network == nil {
			continue
		}
		network.IP = network.IP.To4()
		networks = append(networks, network)
		if candidate.To4().Equal(localIP) {
			owned = true
		}
	}
	if !owned || isKnownIPv4SpecialNetworkAddress(localIP, networks) {
		return nil, errors.New("raw IPv4 address is not a usable interface unicast address")
	}

	conn, err := net.ListenIP("ip4:"+strconv.Itoa(protocol), &net.IPAddr{IP: append(net.IP(nil), localIP...)})
	if err != nil {
		return nil, err
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = conn.Close()
		}
	}()
	if err := setRawIPv4ReceiveBuffer(conn); err != nil {
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
	return &rawIPv4Socket{
		conn: conn, raw: raw, localIP: append(net.IP(nil), localIP...),
		ifIndex: iface.Index, networks: networks, protocol: protocol,
	}, nil
}

func setRawIPv4ReceiveBuffer(conn *net.IPConn) error {
	control, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var socketErr error
	if err := control.Control(func(fd uintptr) {
		// Linux doubles SO_RCVBUF on readback. Request half the desired size.
		socketErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_RCVBUF, rawIPv4ReceiveBuffer/2)
		if socketErr != nil {
			return
		}
		effective, getErr := unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_RCVBUF)
		if getErr != nil {
			socketErr = getErr
			return
		}
		if effective != rawIPv4ReceiveBuffer {
			socketErr = errors.New("kernel raw IPv4 receive queue does not match configured bound")
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

func validRawIPv4Peer(ip net.IP) bool {
	ipv4 := ip.To4()
	addr, ok := netip.AddrFromSlice(ipv4)
	return ok && addr.Is4() && !addr.IsUnspecified() && !addr.IsMulticast() &&
		addr != netip.MustParseAddr("255.255.255.255")
}

func (s *rawIPv4Socket) validInbound(header *ipv4.Header, payload []byte, control *ipv4.ControlMessage) bool {
	if s == nil || header == nil || control == nil || header.Version != 4 || header.Protocol != s.protocol ||
		header.Len != ipv4.HeaderLen || header.TotalLen != header.Len+len(payload) ||
		header.Flags&^ipv4.DontFragment != 0 || header.FragOff != 0 ||
		!header.Dst.To4().Equal(s.localIP) || control.Dst == nil || !control.Dst.Equal(s.localIP) || control.IfIndex != s.ifIndex {
		return false
	}
	return validRawIPv4Peer(header.Src) && !isKnownIPv4SpecialNetworkAddress(header.Src, s.networks)
}

func (s *rawIPv4Socket) writeTo(destination net.IP, payload []byte) error {
	if s == nil || s.raw == nil || s.conn == nil {
		return net.ErrClosed
	}
	header := &ipv4.Header{
		Version: 4, Len: ipv4.HeaderLen, TotalLen: ipv4.HeaderLen + len(payload),
		Flags: ipv4.DontFragment, TTL: 64, Protocol: s.protocol,
		Src: s.localIP, Dst: destination,
	}
	return s.raw.WriteTo(header, payload, &ipv4.ControlMessage{Src: s.localIP, IfIndex: s.ifIndex})
}

func (s *rawIPv4Socket) Close() error {
	if s == nil || s.conn == nil {
		return nil
	}
	s.closeOnce.Do(func() { s.closeError = s.conn.Close() })
	return s.closeError
}

func (s *rawIPv4Socket) SetDeadline(deadline time.Time) error {
	if s == nil || s.conn == nil {
		return net.ErrClosed
	}
	return s.conn.SetDeadline(deadline)
}

func (s *rawIPv4Socket) SetReadDeadline(deadline time.Time) error {
	if s == nil || s.conn == nil {
		return net.ErrClosed
	}
	return s.conn.SetReadDeadline(deadline)
}

func (s *rawIPv4Socket) SetWriteDeadline(deadline time.Time) error {
	if s == nil || s.conn == nil {
		return net.ErrClosed
	}
	return s.conn.SetWriteDeadline(deadline)
}
