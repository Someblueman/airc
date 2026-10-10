//go:build linux

package adapter

import (
	"encoding/binary"
	"errors"
	"net"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

const (
	icmpV4Protocol    = 1
	icmpV4HeaderLen   = 8
	icmpV4MaxDatagram = 1000
)

type ICMPv4Packets struct {
	socket *rawIPv4Socket
	local  ICMPv4Addr
}

func ListenICMPv4(address *ICMPv4Addr, interfaceName string) (*ICMPv4Packets, error) {
	if !validICMPv4Addr(address) {
		return nil, errors.New("invalid ICMPv4 listener address")
	}
	if err := (&config.ICMPv4{Interface: interfaceName}).Validate(address.String()); err != nil {
		return nil, err
	}
	socket, err := listenRawIPv4(address.IP, interfaceName, icmpV4Protocol)
	if err != nil {
		return nil, err
	}
	return &ICMPv4Packets{
		socket: socket,
		local:  ICMPv4Addr{IP: append(net.IP(nil), address.IP...), Channel: address.Channel},
	}, nil
}

func (c *ICMPv4Packets) ReadFrom(body []byte) (int, net.Addr, error) {
	if c == nil || c.socket == nil || c.socket.raw == nil {
		return 0, nil, net.ErrClosed
	}
	var wire [65536]byte
	for {
		header, payload, control, err := c.socket.raw.ReadFrom(wire[:])
		if err != nil {
			return 0, nil, err
		}
		if !c.socket.validInbound(header, payload, control) ||
			len(payload) < icmpV4HeaderLen+1 || len(payload) > icmpV4HeaderLen+icmpV4MaxDatagram ||
			!validICMPv4Checksum(payload) {
			continue
		}
		message, err := icmp.ParseMessage(icmpV4Protocol, payload)
		if err != nil || message.Type != ipv4.ICMPTypeEcho || message.Code != 0 {
			continue
		}
		echo, ok := message.Body.(*icmp.Echo)
		if !ok || echo.ID != int(c.local.Channel) || echo.Seq < 1 || echo.Seq > 65535 || len(echo.Data) == 0 || len(echo.Data) > icmpV4MaxDatagram {
			continue
		}
		if len(body) < len(echo.Data) {
			return 0, nil, errors.New("ICMPv4 read buffer is too small for complete datagram")
		}
		copy(body, echo.Data)
		return len(echo.Data), &ICMPv4Addr{
			IP: append(net.IP(nil), header.Src.To4()...), Channel: uint16(echo.Seq),
		}, nil
	}
}

func validICMPv4Checksum(packet []byte) bool {
	var sum uint32
	for len(packet) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(packet[:2]))
		packet = packet[2:]
	}
	if len(packet) == 1 {
		sum += uint32(packet[0]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return sum == 0xffff
}

func (c *ICMPv4Packets) WriteTo(body []byte, destination net.Addr) (int, error) {
	if c == nil || c.socket == nil || c.socket.raw == nil {
		return 0, net.ErrClosed
	}
	peer, ok := destination.(*ICMPv4Addr)
	if !ok || !validICMPv4Addr(peer) || len(body) == 0 || len(body) > icmpV4MaxDatagram ||
		isKnownIPv4SpecialNetworkAddress(peer.IP, c.socket.networks) ||
		(peer.IP.Equal(c.local.IP) && peer.Channel == c.local.Channel) {
		return 0, errors.New("invalid ICMPv4 destination or datagram size")
	}
	message, err := (&icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Code: 0,
		Body: &icmp.Echo{ID: int(peer.Channel), Seq: int(c.local.Channel), Data: body},
	}).Marshal(nil)
	if err != nil {
		return 0, err
	}
	if err := c.socket.writeTo(peer.IP, message); err != nil {
		return 0, err
	}
	return len(body), nil
}

func (c *ICMPv4Packets) LocalAddr() net.Addr {
	if c == nil {
		return (*ICMPv4Addr)(nil)
	}
	return &ICMPv4Addr{IP: append(net.IP(nil), c.local.IP...), Channel: c.local.Channel}
}

func (c *ICMPv4Packets) Close() error {
	if c == nil || c.socket == nil {
		return nil
	}
	return c.socket.Close()
}

func (c *ICMPv4Packets) SetDeadline(deadline time.Time) error {
	if c == nil || c.socket == nil {
		return net.ErrClosed
	}
	return c.socket.SetDeadline(deadline)
}

func (c *ICMPv4Packets) SetReadDeadline(deadline time.Time) error {
	if c == nil || c.socket == nil {
		return net.ErrClosed
	}
	return c.socket.SetReadDeadline(deadline)
}

func (c *ICMPv4Packets) SetWriteDeadline(deadline time.Time) error {
	if c == nil || c.socket == nil {
		return net.ErrClosed
	}
	return c.socket.SetWriteDeadline(deadline)
}

var _ net.PacketConn = (*ICMPv4Packets)(nil)
