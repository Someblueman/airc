//go:build !linux

package adapter

import (
	"errors"
	"net"
)

func ListenUDPLite(address *net.UDPAddr) (*UDPLitePackets, error) {
	return nil, errors.New("UDP-Lite socket ownership requires Linux")
}
