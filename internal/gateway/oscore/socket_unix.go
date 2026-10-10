//go:build darwin || linux

package oscore

import (
	"net"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func privateSocketPair() (net.Conn, *os.File, error) {
	syscall.ForkLock.RLock()
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err == nil {
		unix.CloseOnExec(fds[0])
		unix.CloseOnExec(fds[1])
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return nil, nil, err
	}
	parentFile := os.NewFile(uintptr(fds[0]), "oscore-owner")
	child := os.NewFile(uintptr(fds[1]), "oscore-native")
	parent, err := net.FileConn(parentFile)
	_ = parentFile.Close()
	if err != nil {
		_ = child.Close()
		return nil, nil, err
	}
	return parent, child, nil
}
