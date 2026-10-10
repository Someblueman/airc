//go:build darwin || linux

package adapter

import (
	"testing"

	"golang.org/x/sys/unix"
)

func TestIPv4MulticastNativeSocketHasNoBroadcastPermission(t *testing.T) {
	owner, _, _ := multicastIPv4Fixture(t)
	raw, err := owner.conn.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	value := -1
	var optionError error
	if err := raw.Control(func(fd uintptr) {
		value, optionError = unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_BROADCAST)
	}); err != nil || optionError != nil || value != 0 {
		t.Fatal("broadcast remains enabled", value, err, optionError)
	}
}
