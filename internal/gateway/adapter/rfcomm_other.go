//go:build !linux

package adapter

import (
	"errors"
	"net"
)

func ListenRFCOMM(string, []string) (net.Listener, error) {
	return nil, errors.New("Bluetooth RFCOMM requires Linux")
}
