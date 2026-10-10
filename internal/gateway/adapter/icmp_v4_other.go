//go:build !linux

package adapter

import (
	"errors"
	"net"
	"time"
)

type ICMPv4Packets struct{}

func ListenICMPv4(*ICMPv4Addr, string) (*ICMPv4Packets, error) {
	return nil, errors.New("ICMPv4 raw sockets require Linux")
}

func (*ICMPv4Packets) ReadFrom([]byte) (int, net.Addr, error) {
	return 0, nil, errors.New("ICMPv4 raw sockets require Linux")
}

func (*ICMPv4Packets) WriteTo([]byte, net.Addr) (int, error) {
	return 0, errors.New("ICMPv4 raw sockets require Linux")
}

func (*ICMPv4Packets) LocalAddr() net.Addr { return nil }
func (*ICMPv4Packets) Close() error        { return nil }

func (*ICMPv4Packets) SetDeadline(time.Time) error {
	return errors.New("ICMPv4 raw sockets require Linux")
}

func (*ICMPv4Packets) SetReadDeadline(time.Time) error {
	return errors.New("ICMPv4 raw sockets require Linux")
}

func (*ICMPv4Packets) SetWriteDeadline(time.Time) error {
	return errors.New("ICMPv4 raw sockets require Linux")
}

var _ net.PacketConn = (*ICMPv4Packets)(nil)
