//go:build !linux

package adapter

import (
	"errors"
	"net"
)

func ListenVSock(string, []uint32) (net.Listener, error) {
	return nil, errors.New("virtio vsock listener requires linux")
}
