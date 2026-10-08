//go:build !darwin && !linux

package adapter

import (
	"errors"
	"net"
)

func disableIPv4MulticastBroadcast(*net.UDPConn) error {
	return errors.New("IPv4 multicast socket ownership requires Darwin or Linux")
}
