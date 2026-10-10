//go:build darwin || linux

package adapter

import (
	"net"

	"golang.org/x/sys/unix"
)

func disableIPv4MulticastBroadcast(conn *net.UDPConn) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var optionError error
	if err := raw.Control(func(fd uintptr) {
		optionError = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_BROADCAST, 0)
	}); err != nil {
		return err
	}
	return optionError
}
