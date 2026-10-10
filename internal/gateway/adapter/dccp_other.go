//go:build !linux

package adapter

import (
	"errors"
	"net"
)

func ListenDCCP(string) (net.Listener, error) {
	return nil, errors.New("dccp listener requires linux")
}
