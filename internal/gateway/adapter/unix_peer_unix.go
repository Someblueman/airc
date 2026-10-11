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

func unixPeerAllowed(conn net.Conn) error {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return errors.New("unix listener accepted non-unix connection")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return err
	}
	var uid uint32
	var socketErr error
	err = raw.Control(func(fd uintptr) {
		uid, socketErr = socketPeerUID(int(fd))
	})
	if err != nil {
		return err
	}
	if socketErr != nil {
		return socketErr
	}
	if uid != uint32(os.Geteuid()) {
		return errUnixPeerUID
	}
	return nil
}
