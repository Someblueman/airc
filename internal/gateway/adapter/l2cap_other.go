//go:build !linux

package adapter

import (
	"errors"
	"net"
)

func ListenL2CAP(string, []string) (net.Listener, error) {
	return nil, errors.New("Bluetooth L2CAP requires Linux")
}
