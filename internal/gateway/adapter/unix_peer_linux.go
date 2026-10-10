//go:build linux

package adapter

import (
	"errors"
	"net"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

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
	var peer *unix.Ucred
	raw, err := uc.SyscallConn()
	if err != nil {
		return err
	}
	var socketErr error
	err = raw.Control(func(fd uintptr) {
		peer, socketErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	})
	if err != nil {
		return err
	}
	if socketErr != nil {
		return socketErr
	}
	if peer == nil || peer.Uid != uint32(os.Geteuid()) {
		return errors.New("unix peer UID is not the gateway UID")
	}
	return nil
}
