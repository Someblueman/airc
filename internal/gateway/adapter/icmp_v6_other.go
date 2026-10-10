//go:build !darwin || !arm64

package adapter

import (
	"errors"
	"net"
	"time"
)

type ICMPv6Packets struct{}

func ListenICMPv6(address *ICMPv6Addr, interfaceName string) (*ICMPv6Packets, error) {
	return nil, errors.New("ICMPv6 datagram sockets require Darwin ARM64")
}

func (*ICMPv6Packets) ReadFrom([]byte) (int, net.Addr, error) {
	return 0, nil, errors.New("ICMPv6 datagram sockets require Darwin ARM64")
}
func (*ICMPv6Packets) WriteTo([]byte, net.Addr) (int, error) {
	return 0, errors.New("ICMPv6 datagram sockets require Darwin ARM64")
}
func (*ICMPv6Packets) LocalAddr() net.Addr { return nil }
func (*ICMPv6Packets) Close() error        { return nil }
func (*ICMPv6Packets) SetDeadline(time.Time) error {
	return errors.New("ICMPv6 datagram sockets require Darwin ARM64")
}
func (*ICMPv6Packets) SetReadDeadline(time.Time) error {
	return errors.New("ICMPv6 datagram sockets require Darwin ARM64")
}
func (*ICMPv6Packets) SetWriteDeadline(time.Time) error {
	return errors.New("ICMPv6 datagram sockets require Darwin ARM64")
}
