//go:build darwin || linux

package adapter

import (
	"errors"
	"net"
	"os"
	"syscall"
)

var errUnixPeerUID = errors.New("unix peer UID is not the gateway UID")

func unixPathOwner(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && uint32(stat.Uid) == uint32(os.Geteuid())
}

func unixPlatformSupported() bool { return true }

func unixTrustedAncestor(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	if stat.Uid != 0 && stat.Uid != uint32(os.Geteuid()) {
		return false
	}
	if info.Mode().Perm()&022 == 0 {
		return true
	}
	return info.Mode()&os.ModeSticky != 0 && stat.Uid == 0
}

// unixPeerAllowed admits a peer running as the gateway's own user and reports
// its process ID.
func unixPeerAllowed(conn net.Conn) (int, error) {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, errors.New("unix listener accepted non-unix connection")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, err
	}
	var uid uint32
	var pid int
	var socketErr error
	err = raw.Control(func(fd uintptr) {
		uid, pid, socketErr = socketPeer(int(fd))
	})
	if err != nil {
		return 0, err
	}
	if socketErr != nil {
		return 0, socketErr
	}
	if uid != uint32(os.Geteuid()) {
		return 0, errUnixPeerUID
	}
	return pid, nil
}
