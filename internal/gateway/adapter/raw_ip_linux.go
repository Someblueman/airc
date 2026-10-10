//go:build linux

package adapter

import (
	"encoding/binary"
	"errors"
	"net"
	"time"

	"golang.org/x/net/ipv4"
)

const (
	rawIPProtocol    = 253
	rawIPHeaderBytes = 8
	rawIPMaxDatagram = 1000
	rawIPWireMagic   = "AGI1"
)

// RawIPPackets owns a bounded IPv4 protocol-253 socket.
type RawIPPackets struct {
	socket *rawIPv4Socket
	local  RawIPAddr
}

func ListenRawIP(address *RawIPAddr, interfaceName string) (*RawIPPackets, error) {
	if !validRawIPAddr(address) {
		return nil, errors.New("invalid raw IP listener address")
	}
	socket, err := listenRawIPv4(address.IP, interfaceName, rawIPProtocol)
	if err != nil {
		return nil, err
	}
	return &RawIPPackets{
		socket: socket,
		local:  RawIPAddr{IP: append(net.IP(nil), address.IP...), Channel: address.Channel},
	}, nil
}

func (c *RawIPPackets) ReadFrom(body []byte) (int, net.Addr, error) {
	if c == nil || c.socket == nil || c.socket.raw == nil {
		return 0, nil, net.ErrClosed
	}
	var wire [65536]byte
	for {
		header, payload, control, err := c.socket.raw.ReadFrom(wire[:])
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
		return len(data), &RawIPAddr{
			IP: append(net.IP(nil), header.Src.To4()...), Channel: binary.BigEndian.Uint16(payload[6:8]),
		}, nil
	}
}

func (c *RawIPPackets) validInbound(header *ipv4.Header, payload []byte, control *ipv4.ControlMessage) bool {
	if c == nil || c.socket == nil || !c.socket.validInbound(header, payload, control) ||
		len(payload) < rawIPHeaderBytes+1 || len(payload) > rawIPHeaderBytes+rawIPMaxDatagram {
		return false
	}
	return string(payload[:4]) == rawIPWireMagic &&
		binary.BigEndian.Uint16(payload[4:6]) == c.local.Channel &&
		binary.BigEndian.Uint16(payload[6:8]) != 0
}

func (c *RawIPPackets) WriteTo(body []byte, destination net.Addr) (int, error) {
	if c == nil || c.socket == nil || c.socket.raw == nil {
		return 0, net.ErrClosed
	}
	peer, ok := destination.(*RawIPAddr)
	if !ok || !validRawIPAddr(peer) || len(body) == 0 || len(body) > rawIPMaxDatagram ||
		isKnownIPv4SpecialNetworkAddress(peer.IP, c.socket.networks) {
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
	if err := c.socket.writeTo(peer.IP, payload); err != nil {
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
	if c == nil || c.socket == nil {
		return nil
	}
	return c.socket.Close()
}

func (c *RawIPPackets) SetDeadline(deadline time.Time) error {
	if c == nil || c.socket == nil {
		return net.ErrClosed
	}
	return c.socket.SetDeadline(deadline)
}

func (c *RawIPPackets) SetReadDeadline(deadline time.Time) error {
	if c == nil || c.socket == nil {
		return net.ErrClosed
	}
	return c.socket.SetReadDeadline(deadline)
}

func (c *RawIPPackets) SetWriteDeadline(deadline time.Time) error {
	if c == nil || c.socket == nil {
		return net.ErrClosed
	}
	return c.socket.SetWriteDeadline(deadline)
}

var _ net.PacketConn = (*RawIPPackets)(nil)
