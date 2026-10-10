//go:build !linux

package adapter

import (
	"errors"
	"net"
	"time"
)

type RawIPPackets struct{}

func ListenRawIP(*RawIPAddr, string) (*RawIPPackets, error) {
	return nil, errors.New("raw IP sockets are supported only on Linux")
}

func (*RawIPPackets) ReadFrom([]byte) (int, net.Addr, error) {
	return 0, nil, errors.New("raw IP sockets are supported only on Linux")
}

func (*RawIPPackets) WriteTo([]byte, net.Addr) (int, error) {
	return 0, errors.New("raw IP sockets are supported only on Linux")
}

func (*RawIPPackets) Close() error { return nil }

func (*RawIPPackets) LocalAddr() net.Addr { return (*RawIPAddr)(nil) }

func (*RawIPPackets) SetDeadline(time.Time) error {
	return errors.New("raw IP sockets are supported only on Linux")
}

func (*RawIPPackets) SetReadDeadline(time.Time) error {
	return errors.New("raw IP sockets are supported only on Linux")
}

func (*RawIPPackets) SetWriteDeadline(time.Time) error {
	return errors.New("raw IP sockets are supported only on Linux")
}

var _ net.PacketConn = (*RawIPPackets)(nil)
