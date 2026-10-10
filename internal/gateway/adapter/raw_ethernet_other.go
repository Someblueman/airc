//go:build !linux

package adapter

import (
	"errors"
	"net"
	"time"
)

type RawEthernetPackets struct{}

func ListenRawEthernet(*RawEthernetAddr, string) (*RawEthernetPackets, error) {
	return nil, errors.New("raw Ethernet sockets require Linux")
}

func (*RawEthernetPackets) ReadFrom([]byte) (int, net.Addr, error) {
	return 0, nil, errors.New("raw Ethernet sockets require Linux")
}

func (*RawEthernetPackets) WriteTo([]byte, net.Addr) (int, error) {
	return 0, errors.New("raw Ethernet sockets require Linux")
}

func (*RawEthernetPackets) LocalAddr() net.Addr { return nil }
func (*RawEthernetPackets) Close() error        { return nil }

func (*RawEthernetPackets) SetDeadline(time.Time) error {
	return errors.New("raw Ethernet sockets require Linux")
}

func (*RawEthernetPackets) SetReadDeadline(time.Time) error {
	return errors.New("raw Ethernet sockets require Linux")
}

func (*RawEthernetPackets) SetWriteDeadline(time.Time) error {
	return errors.New("raw Ethernet sockets require Linux")
}

var _ net.PacketConn = (*RawEthernetPackets)(nil)
